"""Offline workflow helper contracts: tag resolution and immutable build reuse."""
import json
import os
from pathlib import Path
import plistlib
import runpy
import shutil
import stat
import struct
import subprocess
import sys
import tempfile
import unittest
from unittest import mock
import zipfile

import test_publish_release as release_tests

SCRIPTS = Path(__file__).resolve().parent
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
    def invoke(self, responses, required=False):
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp) / 'output'
            with mock.patch.dict(os.environ, GITHUB_REPOSITORY='owner/repo', TAG='v1.2.3', VERSION='1.2.3', RELEASE_COMMIT=COMMIT, GITHUB_OUTPUT=str(output)), mock.patch('subprocess.check_output', side_effect=[json.dumps(r) for r in responses]), mock.patch('subprocess.run') as run, mock.patch('sys.argv', ['restore-release.py'] + (['--required'] if required else [])):
                runpy.run_path(str(SCRIPTS / 'restore-release.py'))
            return output.read_text(), run.call_args_list

    def test_reuses_original_run(self):
        name = 'release-v1.2.3-' + COMMIT
        output, calls = self.invoke([[{'artifacts': [{'name': name, 'expired': False, 'id': 22, 'workflow_run': {'id': 123}}]}]])
        self.assertIn('restored=true', output)
        self.assertEqual(calls[0].args[0][:4], ['gh', 'run', 'download', '123'])

    def test_new_release_can_build(self):
        output, calls = self.invoke([[{'artifacts': []}], [[]]])
        self.assertIn('restored=false', output)
        self.assertEqual(calls, [])

    def test_publish_never_rebuilds(self):
        with self.assertRaisesRegex(SystemExit, 'unavailable'):
            self.invoke([[{'artifacts': []}], [[]]], required=True)

    def test_duplicate_release_rejected(self):
        with self.assertRaisesRegex(SystemExit, 'Duplicate'):
            self.invoke([[{'artifacts': []}], [[{'tag_name': 'v1.2.3'}, {'tag_name': 'v1.2.3'}]]])


class ReleaseAssetContractTests(release_tests.ReleaseFixture, unittest.TestCase):
    def setUp(self):
        super().setUp()
        self.version = release_tests.publisher.VERSION
        self.base = 'Wallpaper-' + self.version
        (self.root / 'LICENSE').write_bytes(b'license-fixture')
        info = self.folder / 'BUILD-INFO.txt'
        info.write_text(info.read_text() + 'production_update_feed=not-configured\n')
        self.package_fixtures()
        self.write_sums()
        # 执行验证器的完整 Python 入口，只替代外部工具，不复制单条断言。
        script = SCRIPTS / 'release-verify.sh'
        source = script.read_text().split("<<'PY'\n", 1)[1].rsplit('\nPY', 1)[0]
        self.verifier = compile(source, str(script), 'exec')

    @staticmethod
    def pe_fixture(machine):
        data = bytearray(512)
        data[:2] = b'MZ'
        struct.pack_into('<I', data, 0x3c, 0x80)
        data[0x80:0x84] = b'PE\0\0'
        struct.pack_into('<H', data, 0x84, machine)
        struct.pack_into('<H', data, 0x98, 0x10b if machine == 0x14c else 0x20b)
        struct.pack_into('<H', data, 0x98 + 68, 2)
        return bytes(data)

    def package_fixtures(self):
        metadata = self.root / 'dist/package-metadata'
        metadata.mkdir(parents=True, exist_ok=True)
        for name in ('BUILD-INFO.txt', 'RELEASE-NOTES.txt'):
            shutil.copyfile(self.folder / name, metadata / name)
        shutil.copyfile(self.root / 'LICENSE', metadata / 'LICENSE')
        (metadata / 'THIRD-PARTY-NOTICES.txt').write_bytes(b'THIRD-PARTY NOTICES fixture')
        for arch, machine in (('amd64', 0x8664), ('arm64', 0xaa64)):
            binary = self.root / 'dist' / ('windows-' + arch) / 'wallpaperd.exe'
            binary.parent.mkdir(parents=True, exist_ok=True)
            binary.write_bytes(self.pe_fixture(machine))
            installer = self.folder / (self.base + '-windows-' + arch + '-setup.exe')
            installer.write_bytes(self.pe_fixture(0x14c))
            # 用可解包的 ZIP 夹具承载 PE 和元数据，7z 调用在测试边界替代。
            with zipfile.ZipFile(installer, 'a') as archive:
                archive.write(binary, 'wallpaperd.exe')
                for path in metadata.iterdir():
                    archive.write(path, path.name)
        for arch, cpu in (('amd64', 0x1000007), ('arm64', 0x100000c)):
            binary = self.root / 'dist' / ('darwin-' + arch) / 'wallpaperd'
            binary.parent.mkdir(parents=True, exist_ok=True)
            binary.write_bytes(struct.pack('<II', 0xfeedfacf, cpu) + bytes(24))
            with zipfile.ZipFile(self.folder / (self.base + '-macos-' + arch + '.app.zip'), 'w') as archive:
                executable = zipfile.ZipInfo('Wallpaper.app/Contents/MacOS/wallpaperd')
                executable.create_system = 3
                executable.external_attr = (stat.S_IFREG | 0o755) << 16
                archive.writestr(executable, binary.read_bytes())
                archive.writestr('Wallpaper.app/Contents/Info.plist', plistlib.dumps({
                    'CFBundleExecutable': 'wallpaperd',
                    'CFBundleShortVersionString': self.version.split('-')[0],
                    'CFBundleVersion': self.version.split('-')[0],
                    'LSUIElement': True,
                }))
                for path in metadata.iterdir():
                    archive.write(path, 'Wallpaper.app/Contents/Resources/' + path.name)

    def verify(self):
        def external_tool(command, **kwargs):
            self.assertTrue(kwargs['check'])
            if command[:2] == ['hdiutil', 'verify']:
                self.assertTrue(Path(command[2]).is_file())
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
                    if signed:
                        extras.append(self.base + '-updates.tar.gz')
                    for name in extras:
                        (self.folder / name).write_bytes(b'optional-artifact-fixture')
                    try:
                        self.package_fixtures()
                        self.write_sums()
                        self.verify()
                        assets = release_tests.publisher.expected_assets(self.folder)
                        self.assertEqual(set(assets), {p.name for p in self.folder.iterdir()})
                        self.assertEqual(len(assets), 7 + len(extras))
                    finally:
                        for name in extras:
                            (self.folder / name).unlink()

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


if __name__ == '__main__':
    unittest.main()
