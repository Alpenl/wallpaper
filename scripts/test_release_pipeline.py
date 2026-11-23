"""Offline workflow helper contracts: tag resolution, immutable build reuse, and asset verification."""
import io
import json
import os
from pathlib import Path
import plistlib
import re
import runpy
import shutil
import stat
import struct
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest import mock
import zipfile

import test_publish_release as release_tests

SCRIPTS = Path(__file__).resolve().parent
REPO_ROOT = SCRIPTS.parent
COMMIT = 'a' * 40


class ContextTests(unittest.TestCase):
    def test_annotated_tag_is_peeled_and_ancestry_is_required(self):
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp) / 'output'
            with mock.patch.dict(os.environ, TAG='v3.2.1-rc.4', GITHUB_OUTPUT=str(output)), mock.patch('subprocess.run') as run, mock.patch('subprocess.check_output', return_value=COMMIT + '\n') as read:
                runpy.run_path(str(SCRIPTS / 'release-context.py'))
            self.assertIn('refs/tags/v3.2.1-rc.4^{commit}', read.call_args.args[0])
            self.assertEqual(run.call_args.args[0], ['git', 'merge-base', '--is-ancestor', COMMIT, 'origin/master'])
            self.assertTrue(run.call_args.kwargs['check'])
            self.assertIn('version=3.2.1-rc.4', output.read_text())

    def test_invalid_tag_never_reaches_git(self):
        with mock.patch.dict(os.environ, TAG='v1.2.3\ncommit=evil'), mock.patch('subprocess.run') as run:
            with self.assertRaises(RuntimeError):
                runpy.run_path(str(SCRIPTS / 'release-context.py'))
            run.assert_not_called()

    def test_nonancestor_stops_without_outputs(self):
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp) / 'output'
            with mock.patch.dict(os.environ, TAG='v1.2.3', GITHUB_OUTPUT=str(output)), mock.patch('subprocess.run', side_effect=subprocess.CalledProcessError(1, 'git')), mock.patch('subprocess.check_output', return_value=COMMIT):
                with self.assertRaises(subprocess.CalledProcessError):
                    runpy.run_path(str(SCRIPTS / 'release-context.py'))
            self.assertFalse(output.exists())


class RestoreTests(unittest.TestCase):
    def invoke(self, responses, extra_args=()):
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp) / 'output'
            with mock.patch.dict(os.environ, GITHUB_REPOSITORY='owner/repo', TAG='v1.2.3', VERSION='1.2.3', RELEASE_COMMIT=COMMIT, GITHUB_OUTPUT=str(output)), mock.patch('subprocess.check_output', side_effect=[json.dumps(r) for r in responses]), mock.patch('subprocess.run') as run, mock.patch('sys.argv', ['restore-release.py', *extra_args]):
                runpy.run_path(str(SCRIPTS / 'restore-release.py'))
            return output.read_text(), run.call_args_list

    def test_reuses_original_run(self):
        name = 'release-v1.2.3-' + COMMIT
        output, calls = self.invoke([[{'artifacts': [{'name': name, 'expired': False, 'id': 22, 'workflow_run': {'id': 123}}]}]])
        self.assertIn('restored=true', output)
        self.assertIn('needs_build=false', output)
        self.assertEqual(calls[0].args[0][:4], ['gh', 'run', 'download', '123'])

    def test_new_release_can_build(self):
        output, calls = self.invoke([[{'artifacts': []}], [[]]])
        self.assertIn('restored=false', output)
        self.assertIn('needs_build=true', output)
        self.assertEqual(calls, [])

    def test_publish_never_rebuilds(self):
        with self.assertRaisesRegex(SystemExit, 'unavailable'):
            self.invoke([[{'artifacts': []}], [[]]], extra_args=('--required',))

    def test_duplicate_release_rejected(self):
        with self.assertRaisesRegex(SystemExit, 'Duplicate'):
            self.invoke([[{'artifacts': []}], [[{'tag_name': 'v1.2.3'}, {'tag_name': 'v1.2.3'}]]])

    def test_check_mode_reuses_artifact_without_downloading(self):
        name = 'release-v1.2.3-' + COMMIT
        output, calls = self.invoke([[{'artifacts': [{'name': name, 'expired': False, 'id': 22, 'workflow_run': {'id': 123}}]}]], extra_args=('--check',))
        self.assertIn('restored=true', output)
        self.assertIn('needs_build=false', output)
        self.assertEqual(calls, [])

    def test_check_mode_refuses_empty_draft_without_artifact(self):
        with self.assertRaisesRegex(SystemExit, 'Partial draft'):
            self.invoke([[{'artifacts': []}], [[{'tag_name': 'v1.2.3', 'draft': True, 'assets': []}]]], extra_args=('--check',))

    def test_check_mode_allows_new_release_to_build(self):
        output, calls = self.invoke([[{'artifacts': []}], [[]]], extra_args=('--check',))
        self.assertIn('restored=false', output)
        self.assertIn('needs_build=true', output)
        self.assertEqual(calls, [])


class ReleaseAssetContractTests(release_tests.ReleaseFixture, unittest.TestCase):
    def setUp(self):
        super().setUp()
        self.version = release_tests.publisher.VERSION
        self.base = 'Wallpaper-' + self.version
        (self.root / 'LICENSE').write_bytes(b'license-fixture')
        icon = self.root / 'assets/icon'
        icon.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(REPO_ROOT / 'assets/icon/AppIcon.icns', icon / 'AppIcon.icns')
        info = self.folder / 'BUILD-INFO.txt'
        info.write_text(info.read_text() + 'production_update_feed=not-configured\n')
        self.package_fixtures()
        self.write_sums()
        # 执行验证器的完整 Python 入口，只替代外部工具，不复制单条断言。
        script = SCRIPTS / 'release-verify.sh'
        source = script.read_text().split("<<'PY'\n", 1)[1].rsplit('\nPY', 1)[0]
        self.verifier = compile(source, str(script), 'exec')

    @staticmethod
    def pe_fixture(machine, signed=False):
        pe32plus = machine != 0x14c
        optional_size = 240 if pe32plus else 224
        data = bytearray(0x300)
        data[:2] = b'MZ'
        struct.pack_into('<I', data, 0x3c, 0x80)
        data[0x80:0x84] = b'PE\0\0'
        struct.pack_into('<H', data, 0x84, machine)
        struct.pack_into('<H', data, 0x86, 1)        # one section (.rsrc)
        struct.pack_into('<H', data, 0x94, optional_size)
        struct.pack_into('<H', data, 0x98, 0x20b if pe32plus else 0x10b)
        struct.pack_into('<H', data, 0x98 + 68, 2)   # GUI subsystem
        directory = 0x98 + (112 if pe32plus else 96)
        struct.pack_into('<II', data, directory + 8 * 2, 0x1000, 40)  # resources
        if signed:
            cert_blob = b'PKCS7-SIGNATURE-FIXTURE'
            cert_offset = len(data)
            struct.pack_into('<II', data, directory + 8 * 4, cert_offset, len(cert_blob))
            data.extend(cert_blob)
        section = 0x98 + optional_size
        data[section:section + 8] = b'.rsrc\0\0\0'
        struct.pack_into('<IIIIIIHHI', data, section + 8,
                         40, 0x1000, 40, 0x200, 0, 0, 0, 0, 0x40000040)
        # Minimal resource tree: one RT_GROUP_ICON (type 14) entry.
        struct.pack_into('<HH', data, 0x200 + 12, 0, 1)
        struct.pack_into('<II', data, 0x210, 14, 0x80000000 | 24)
        return bytes(data)

    @staticmethod
    def macho_fixture(cpu, signed=False):
        if not signed:
            return struct.pack('<II', 0xfeedfacf, cpu) + bytes(24)
        # 64-bit Mach-O header (32 bytes) + 1 LC_CODE_SIGNATURE (16 bytes) + signature blob.
        sig_blob = b'MACHO-CODE-SIGNATURE-FIXTURE'
        header = struct.pack('<IIIIIIII', 0xfeedfacf, cpu, 0, 2, 1, 16, 0, 0)
        lc = struct.pack('<IIII', 0x1d, 16, 48, len(sig_blob))
        return header + lc + sig_blob

    def package_fixtures(self, *, win_signed=False, macos_signed=False, bundle_version=None):
        metadata = self.root / 'dist/package-metadata'
        metadata.mkdir(parents=True, exist_ok=True)
        for name in ('BUILD-INFO.txt', 'RELEASE-NOTES.txt'):
            shutil.copyfile(self.folder / name, metadata / name)
        shutil.copyfile(self.root / 'LICENSE', metadata / 'LICENSE')
        (metadata / 'THIRD-PARTY-NOTICES.txt').write_bytes(b'THIRD-PARTY NOTICES fixture')
        self.win_payloads = {}
        self.darwin_payloads = {}
        self.darwin_plists = {}
        for arch, machine in (('amd64', 0x8664), ('arm64', 0xaa64)):
            binary = self.root / 'dist' / ('windows-' + arch) / 'wallpaperd.exe'
            binary.parent.mkdir(parents=True, exist_ok=True)
            raw_pe = self.pe_fixture(machine, signed=False)
            binary.write_bytes(raw_pe)
            packaged_pe = self.pe_fixture(machine, signed=win_signed)
            staged = self.root / 'dist' / ('windows-packaged-' + arch) / 'wallpaperd.exe'
            staged.parent.mkdir(parents=True, exist_ok=True)
            staged.write_bytes(packaged_pe)
            self.win_payloads[arch] = packaged_pe
            installer = self.folder / (self.base + '-windows-' + arch + '-setup.exe')
            installer.write_bytes(self.pe_fixture(0x14c, signed=win_signed))
            # 用可解包的 ZIP 夹具承载 PE 和元数据，7z 调用在测试边界替代。
            with zipfile.ZipFile(installer, 'a') as archive:
                archive.write(staged, 'wallpaperd.exe')
                for path in metadata.iterdir():
                    archive.write(path, path.name)
        for arch, cpu in (('amd64', 0x1000007), ('arm64', 0x100000c)):
            binary = self.root / 'dist' / ('darwin-' + arch) / 'wallpaperd'
            binary.parent.mkdir(parents=True, exist_ok=True)
            binary.write_bytes(self.macho_fixture(cpu, signed=False))
            packaged_macho = self.macho_fixture(cpu, signed=macos_signed)
            self.darwin_payloads[arch] = packaged_macho
            plist_bytes = plistlib.dumps({
                'CFBundleExecutable': 'wallpaperd',
                'CFBundleIconFile': 'AppIcon.icns',
                'CFBundleShortVersionString': self.version.split('-')[0],
                'CFBundleVersion': bundle_version if bundle_version is not None else self.version,
                'LSUIElement': True,
                'NSScreenCaptureUsageDescription': 'Audio-reactive wallpapers',
            })
            self.darwin_plists[arch] = plist_bytes
            with zipfile.ZipFile(self.folder / (self.base + '-macos-' + arch + '.app.zip'), 'w') as archive:
                executable = zipfile.ZipInfo('Wallpaper.app/Contents/MacOS/wallpaperd')
                executable.create_system = 3
                executable.external_attr = (stat.S_IFREG | 0o755) << 16
                archive.writestr(executable, packaged_macho)
                archive.writestr('Wallpaper.app/Contents/Info.plist', plist_bytes)
                archive.write(self.root / 'assets/icon/AppIcon.icns',
                              'Wallpaper.app/Contents/Resources/AppIcon.icns')
                for path in metadata.iterdir():
                    archive.write(path, 'Wallpaper.app/Contents/Resources/' + path.name)
                if macos_signed:
                    archive.writestr('Wallpaper.app/Contents/_CodeSignature/CodeResources', b'<plist>sig</plist>')

    def write_updates_tar(self, *, flat_darwin=False, unsigned_win=False, macos_signed=False):
        metadata = self.root / 'dist/package-metadata'
        out = self.folder / (self.base + '-updates.tar.gz')
        def add_bytes(tar, rel, payload):
            info = tarfile.TarInfo('./' + rel)
            info.size = len(payload)
            tar.addfile(info, io.BytesIO(payload))
        with tarfile.open(out, 'w:gz') as tar:
            for arch, machine in (('amd64', 0x8664), ('arm64', 0xaa64)):
                prefix = f'windows-{arch}/'
                add_bytes(tar, prefix + 'manifest.json', b'{"schema":1}')
                add_bytes(tar, prefix + 'manifest.json.sig', b'sig')
                exe_bytes = self.pe_fixture(machine, signed=False) if unsigned_win else self.win_payloads[arch]
                add_bytes(tar, prefix + 'wallpaperd.exe', exe_bytes)
                add_bytes(tar, prefix + 'LICENSE', (metadata / 'LICENSE').read_bytes())
                add_bytes(tar, prefix + 'THIRD-PARTY-NOTICES.txt', (metadata / 'THIRD-PARTY-NOTICES.txt').read_bytes())
            for arch in ('amd64', 'arm64'):
                prefix = f'darwin-{arch}/'
                add_bytes(tar, prefix + 'manifest.json', b'{"schema":1}')
                add_bytes(tar, prefix + 'manifest.json.sig', b'sig')
                if flat_darwin:
                    add_bytes(tar, prefix + 'wallpaperd', self.darwin_payloads[arch])
                    add_bytes(tar, prefix + 'LICENSE', (metadata / 'LICENSE').read_bytes())
                    add_bytes(tar, prefix + 'THIRD-PARTY-NOTICES.txt', (metadata / 'THIRD-PARTY-NOTICES.txt').read_bytes())
                else:
                    add_bytes(tar, prefix + 'MacOS/wallpaperd', self.darwin_payloads[arch])
                    add_bytes(tar, prefix + 'Info.plist', self.darwin_plists[arch])
                    add_bytes(tar, prefix + 'Resources/LICENSE', (metadata / 'LICENSE').read_bytes())
                    add_bytes(tar, prefix + 'Resources/THIRD-PARTY-NOTICES.txt', (metadata / 'THIRD-PARTY-NOTICES.txt').read_bytes())
                    if macos_signed:
                        add_bytes(tar, prefix + '_CodeSignature/CodeResources', b'<plist>sig</plist>')

    def verify(self):
        def external_tool(command, **kwargs):
            self.assertTrue(kwargs['check'])
            if command[:2] == ['hdiutil', 'verify']:
                self.assertTrue(Path(command[2]).is_file())
            elif command[:2] == ['ditto', '-x']:
                with zipfile.ZipFile(command[3]) as archive:
                    archive.extractall(command[4])
            elif command[:2] == ['codesign', '--verify']:
                self.assertTrue(Path(command[-1]).is_dir())
            elif command[:2] == ['codesign', '--remove-signature']:
                pass
            else:
                self.assertEqual(command[:3], ['7z', 'x', '-y'])
                with zipfile.ZipFile(command[4]) as archive:
                    archive.extractall(command[3][2:])
            return subprocess.CompletedProcess(command, 0)

        def git_head(command, **kwargs):
            self.assertEqual(command, ['git', 'rev-parse', 'HEAD'])
            return COMMIT + '\n'

        previous = Path.cwd()
        try:
            os.chdir(self.root)
            with mock.patch.object(sys, 'argv', ['release-verify.sh', str(self.folder), self.version]), \
                    mock.patch('subprocess.run', side_effect=external_tool), \
                    mock.patch('subprocess.check_output', side_effect=git_head), \
                    mock.patch('shutil.which', return_value='7z'), mock.patch('builtins.print'):
                exec(self.verifier, {'__name__': '__main__'})
        finally:
            os.chdir(previous)

    def test_four_target_assets_and_optional_artifacts_match_publisher(self):
        info = self.folder / 'BUILD-INFO.txt'
        original = info.read_text()
        for signed in (False, True):
            for arches in ((), ('amd64',), ('arm64',), ('amd64', 'arm64')):
                with self.subTest(signed=signed, dmgs=arches):
                    info.write_text(original + ('updater_signing=signed\n' if signed else 'updater_signing=unsigned\n'))
                    extras = [self.base + '-macos-' + arch + '.dmg' for arch in arches]
                    for name in extras:
                        (self.folder / name).write_bytes(b'optional-artifact-fixture')
                    try:
                        self.package_fixtures()
                        if signed:
                            extras.append(self.base + '-updates.tar.gz')
                            self.write_updates_tar()
                        self.write_sums()
                        self.verify()
                        assets = release_tests.publisher.expected_assets(self.folder)
                        self.assertEqual(set(assets), {p.name for p in self.folder.iterdir()})
                        self.assertEqual(len(assets), 7 + len(extras))
                    finally:
                        for name in extras:
                            (self.folder / name).unlink(missing_ok=True)

    def test_flat_darwin_update_layout_rejected(self):
        info = self.folder / 'BUILD-INFO.txt'
        info.write_text(info.read_text() + 'updater_signing=signed\n')
        self.package_fixtures()
        self.write_updates_tar(flat_darwin=True)
        self.write_sums()
        with self.assertRaisesRegex(AssertionError, 'flat wallpaperd layout'):
            self.verify()

    def test_signed_release_requires_authenticode_and_macos_signature(self):
        info = self.folder / 'BUILD-INFO.txt'
        signed_meta = (
            f'version={self.version}\ncommit={COMMIT}\ndirty=false\n'
            'windows_signing=requested; see external signature verification\n'
            'macos_developer_id=requested; see external signature verification\n'
            'updater_signing=signed\n'
            'production_update_feed=not-configured\n'
        )
        info.write_text(signed_meta)
        # Valid signed Windows + macOS + updates bundle must pass (force non-darwin path for deterministic cross-platform check).
        self.package_fixtures(win_signed=True, macos_signed=True)
        self.write_updates_tar(macos_signed=True)
        self.write_sums()
        with mock.patch.object(sys, 'platform', 'linux'):
            self.verify()
        # Unsigned Windows payload in updates.tar.gz must be rejected when windows_signing is signed.
        self.write_updates_tar(unsigned_win=True, macos_signed=True)
        self.write_sums()
        with mock.patch.object(sys, 'platform', 'linux'):
            with self.assertRaisesRegex(AssertionError, 'Authenticode'):
                self.verify()
        # Unsigned Windows installer/payload must be rejected when windows_signing is signed.
        self.package_fixtures(win_signed=False, macos_signed=True)
        self.write_updates_tar(macos_signed=True)
        self.write_sums()
        with mock.patch.object(sys, 'platform', 'linux'):
            with self.assertRaisesRegex(AssertionError, 'Authenticode'):
                self.verify()
        # Missing macOS _CodeSignature must be rejected when macos_developer_id is signed.
        self.package_fixtures(win_signed=True, macos_signed=False)
        self.write_updates_tar(macos_signed=False)
        self.write_sums()
        with mock.patch.object(sys, 'platform', 'linux'):
            with self.assertRaisesRegex(AssertionError, 'LC_CODE_SIGNATURE|_CodeSignature'):
                self.verify()

    def test_prerelease_cfbundleversion_must_not_strip_suffix(self):
        self.package_fixtures(bundle_version=self.version.split('-')[0])
        self.write_sums()
        with self.assertRaises(AssertionError):
            self.verify()

    def test_each_required_package_is_required_by_both_validators(self):
        for suffix in release_tests.REQUIRED_PACKAGES:
            path = self.folder / (self.base + '-' + suffix)
            original = path.read_bytes()
            with self.subTest(package=suffix):
                path.unlink()
                self.write_sums()
                try:
                    with self.assertRaisesRegex(AssertionError, 'Unexpected asset set') as failure:
                        self.verify()
                    self.assertIn(path.name, str(failure.exception))
                    with self.assertRaisesRegex(RuntimeError, 'Unexpected release asset set') as failure:
                        release_tests.publisher.expected_assets(self.folder)
                    self.assertIn(path.name, str(failure.exception))
                finally:
                    path.write_bytes(original)

    def test_linux_package_is_rejected_by_both_validators(self):
        (self.folder / (self.base + '-linux-amd64.tar.gz')).write_bytes(b'obsolete')
        self.write_sums()
        with self.assertRaisesRegex(AssertionError, 'Unexpected asset set'):
            self.verify()
        with self.assertRaisesRegex(RuntimeError, 'Unexpected release asset set'):
            release_tests.publisher.expected_assets(self.folder)

    def test_tampered_package_is_rejected_by_both_validators(self):
        path = self.folder / (self.base + '-macos-arm64.app.zip')
        path.write_bytes(path.read_bytes() + b'tampered')
        with self.assertRaisesRegex(AssertionError, 'checksum mismatch'):
            self.verify()
        with self.assertRaisesRegex(RuntimeError, 'Checksum mismatch'):
            release_tests.publisher.expected_assets(self.folder)


class PackagingAndWorkflowContractTests(unittest.TestCase):
    def test_version_boundary_regex_rejects_prerelease_and_module_substrings(self):
        common = (SCRIPTS / 'package-common.sh').read_text(encoding='utf-8')
        match = re.search(r"pattern = re\.compile\((.+)\)", common)
        self.assertIsNotNone(match)
        def matches(blob, version):
            pattern = re.compile(rb'(?<![0-9a-z._/v@:-])' + re.escape(version.encode()) + rb'(?![0-9A-Za-z._/+:-])')
            return bool(pattern.search(blob))
        self.assertTrue(matches(b'\x00\x00\x80F1.2.3\x00\x01', '1.2.3'))
        self.assertTrue(matches(b'\x00\x00\x00\x001.2.3-rc.1\x00\x00', '1.2.3-rc.1'))
        self.assertFalse(matches(b'\x00\x00\x00\x001.2.3-rc.1\x00\x00', '1.2.3'))
        self.assertFalse(matches(b'typesetting\tv0.3.5\th1:abc', '0.3.5'))
        self.assertFalse(matches(b'erf.func1.2.3\x00wpapp', '1.2.3'))
        self.assertFalse(matches(b'\x001.2.30\x00', '1.2.3'))

    def test_nsis_installer_contracts(self):
        nsi = (REPO_ROOT / 'packaging/windows/installer.nsi').read_text(encoding='utf-8')
        self.assertEqual(nsi.count('RMDir /r "$INSTDIR\\.update"'), 2)
        self.assertNotIn('RMDir /r "$INSTDIR\\mpv"', nsi)
        self.assertIn('InstallDirRegKey HKCU "${UNINSTKEY}" "InstallLocation"', nsi)
        self.assertIn('IsWow64Process2', nsi)
        self.assertIn('taskkill /F /T /IM wallpaperd.exe', nsi)

    def test_info_plist_template_contracts(self):
        tmpl = (REPO_ROOT / 'packaging/macos/Info.plist.tmpl').read_text(encoding='utf-8')
        self.assertIn('@SHORT_VERSION@', tmpl)
        self.assertIn('@BUNDLE_VERSION@', tmpl)
        self.assertIn('NSScreenCaptureUsageDescription', tmpl)

    def test_workflow_and_signing_contracts(self):
        ci = (REPO_ROOT / '.github/workflows/ci.yml').read_text(encoding='utf-8')
        self.assertIn('go test -race ./...', ci)
        self.assertNotIn('paths-ignore', ci)
        self.assertNotIn('-unsafeptr=false', ci)

        release_wf = (REPO_ROOT / '.github/workflows/release.yml').read_text(encoding='utf-8')
        self.assertIn('preflight:', release_wf)
        self.assertIn('restore-release.py --check', release_wf)
        # Ensure build job stays least-privilege (contents: read) after preflight.
        build_section = release_wf.split('  build:\n', 1)[1].split('  publish:\n', 1)[0]
        self.assertIn('contents: read', build_section)
        self.assertNotIn('contents: write', build_section)

        ci_rel = (SCRIPTS / 'ci-release.sh').read_text(encoding='utf-8')
        self.assertIn('-h sha256', ci_rel)
        self.assertIn('-ts ', ci_rel)

        sign_win = (SCRIPTS / 'sign-windows.sh').read_text(encoding='utf-8')
        self.assertNotIn('${WPAPP_SIGN_CMD//\\{\\}/$f}', sign_win)
        self.assertIn('WPAPP_SIGN_FILE="$f"', sign_win)

        fetch_mpv = (SCRIPTS / 'fetch-mpv.sh').read_text(encoding='utf-8')
        self.assertIn('-v3-', fetch_mpv)
        self.assertIn('EXPECTED_SHA256', fetch_mpv)
        self.assertIn('refusing unpinned release tag', fetch_mpv)


if __name__ == '__main__':
    unittest.main()
