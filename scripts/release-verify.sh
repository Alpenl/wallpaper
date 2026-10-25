#!/usr/bin/env bash
# Inspect packaged bytes without executing Windows/macOS payloads or installers.
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/package-common.sh
package_validate
python3 - "$RELEASE_DIR" "$VERSION" <<'PY'
from pathlib import Path, PurePosixPath
import hashlib, os, plistlib, re, shutil, stat, struct, subprocess, sys, tarfile, tempfile, zipfile
root, version = Path(sys.argv[1]), sys.argv[2]
base = 'Wallpaper-' + version
expected = {base + '-windows-' + a + '-setup.exe' for a in ['amd64', 'arm64']}
expected |= {base + '-macos-' + a + '.app.zip' for a in ['amd64', 'arm64']}
expected |= {base + '-linux-amd64.tar.gz', 'BUILD-INFO.txt', 'RELEASE-NOTES.txt', 'SHA256SUMS'}
if 'updater_signing=signed' in (root / 'BUILD-INFO.txt').read_text().splitlines():
    expected.add(base + '-updates.tar.gz')
actual = {p.name for p in root.iterdir()}
for arch in ['amd64', 'arm64']:
    dmg = base + '-macos-' + arch + '.dmg'
    if dmg in actual:
        expected.add(dmg)
        if sys.platform == 'darwin':
            subprocess.run(['hdiutil', 'verify', str(root / dmg)], check=True)

assert actual == expected, f'Unexpected asset set: missing={expected-actual}, extra={actual-expected}'
for name in expected:
    path = root / name
    assert path.is_file() and not path.is_symlink() and path.stat().st_size, f'Invalid asset: {name}'
sums = {}
for line in (root / 'SHA256SUMS').read_text().splitlines():
    match = re.fullmatch(r'([0-9a-f]{64})  ([A-Za-z0-9_.-]+)', line)
    assert match and match[2] not in sums, 'Malformed/duplicate checksum record'
    sums[match[2]] = match[1]
assert set(sums) == expected - {'SHA256SUMS'}, 'Checksum coverage differs from asset set'
for name, digest in sums.items():
    assert hashlib.sha256((root / name).read_bytes()).hexdigest() == digest, name + ': checksum mismatch'
metadata = (root / 'BUILD-INFO.txt').read_text()
assert 'version=' + version in metadata.splitlines(), 'Version provenance mismatch'
assert 'commit=' + subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip() in metadata.splitlines(), 'Commit provenance mismatch'
assert 'production_update_feed=not-configured' in metadata.splitlines()
if os.environ.get('GITHUB_SHA'):
    assert 'dirty=false' in metadata.splitlines(), 'Dirty CI release provenance'
assert Path('dist/package-metadata/LICENSE').read_bytes() == Path('LICENSE').read_bytes(), 'App license differs'
notices = Path('dist/package-metadata/THIRD-PARTY-NOTICES.txt').read_bytes()
assert notices and b'THIRD-PARTY NOTICES' in notices

def pe(data, machine, unsigned=False):
    assert data[:2] == b'MZ', 'Not PE'
    offset = struct.unpack_from('<I', data, 0x3c)[0]
    assert data[offset:offset+4] == b'PE\0\0', 'Invalid PE signature'
    assert struct.unpack_from('<H', data, offset+4)[0] == machine, 'Wrong PE architecture'
    optional = offset + 24
    assert struct.unpack_from('<H', data, optional+68)[0] == 2, 'Not a Windows GUI binary'
    if unsigned:
        magic = struct.unpack_from('<H', data, optional)[0]
        directory = optional + (112 if magic == 0x20b else 96)
        assert struct.unpack_from('<II', data, directory+8*4) == (0, 0), 'Unexpected Authenticode signature'

def same_payload(data, target):
    binary = Path('dist') / target / ('wallpaperd.exe' if target.startswith('windows-') else 'wallpaperd')
    # Windows signing hook may alter a staged binary, but unsigned release must match exactly.
    signed = ((target.startswith('windows-') and 'windows_signing=unsigned' not in metadata.splitlines()) or
              (target.startswith('darwin-') and 'macos_developer_id=none' not in metadata.splitlines()))
    if not signed:
        original = binary.read_bytes()
        if target.startswith('darwin-') and data != original and sys.platform == 'darwin':
            # Bundle sealing replaces Go's ad-hoc signature; compare both payloads
            # after removing signatures, rather than skipping provenance checks.
            with tempfile.TemporaryDirectory(prefix='wallpaper-signature-check-') as temp:
                normalized = []
                for name, content in [('packaged', data), ('built', original)]:
                    path = Path(temp) / name
                    path.write_bytes(content)
                    subprocess.run(['codesign', '--remove-signature', str(path)], check=True)
                    content = bytearray(path.read_bytes())
                    # codesign leaves the previous __LINKEDIT virtual size behind;
                    # it covers the replaced signature, not executable instructions.
                    offset = 32
                    for _ in range(struct.unpack_from('<I', content, 16)[0]):
                        command, size = struct.unpack_from('<II', content, offset)
                        if command == 0x19 and content[offset+8:offset+24].rstrip(b'\0') == b'__LINKEDIT':
                            struct.pack_into('<Q', content, offset+32, 0)
                        offset += size
                    normalized.append(content)
                assert normalized[0] == normalized[1], target + ': code differs beyond signature'
        else:
            assert data == original, target + ': packaged binary differs from build'

extractor = shutil.which('7z') or shutil.which('7zz')
assert extractor, 'Need 7z or 7zz to independently verify NSIS payloads (Ubuntu: p7zip-full)'
for arch, machine in [('amd64', 0x8664), ('arm64', 0xaa64)]:
    installer = root / (base + '-windows-' + arch + '-setup.exe')
    unsigned = 'windows_signing=unsigned' in metadata.splitlines()
    # NSIS uses an x86 bootstrap for both native payload architectures.
    pe(installer.read_bytes(), 0x14c, unsigned)
    with tempfile.TemporaryDirectory(prefix='wallpaper-nsis-check-') as temp:
        subprocess.run([extractor, 'x', '-y', '-o' + temp, str(installer)], check=True, stdout=subprocess.DEVNULL)
        extracted = Path(temp)
        candidates = list(extracted.rglob('wallpaperd.exe'))
        assert len(candidates) == 1, 'Missing/ambiguous NSIS native payload'
        data = candidates[0].read_bytes()
        pe(data, machine, unsigned)
        same_payload(data, 'windows-' + arch)
        assert not list(extracted.rglob('mpv.exe')), 'Unexpected bundled mpv'
        for name in ['LICENSE', 'RELEASE-NOTES.txt', 'BUILD-INFO.txt', 'THIRD-PARTY-NOTICES.txt']:
            matches = list(extracted.rglob(name))
            assert len(matches) == 1 and matches[0].read_bytes() == (Path('dist/package-metadata') / name).read_bytes(), 'NSIS metadata differs: ' + name
for arch, cpu in [('amd64', 0x1000007), ('arm64', 0x100000c)]:
    with zipfile.ZipFile(root / (base + '-macos-' + arch + '.app.zip')) as archive:
        assert archive.testzip() is None, 'ZIP CRC failure'
        assert len(archive.namelist()) == len(set(archive.namelist())), 'Duplicate ZIP members'
        for name in archive.namelist():
            assert not name.startswith('/') and '..' not in PurePosixPath(name).parts, 'Unsafe ZIP path'
        executable = 'Wallpaper.app/Contents/MacOS/wallpaperd'
        info = archive.getinfo(executable)
        assert stat.S_ISREG(info.external_attr >> 16) and info.external_attr >> 16 & 0o111, 'Missing executable permissions'
        data = archive.read(executable)
        assert data[:4] == b'\xcf\xfa\xed\xfe' and struct.unpack_from('<I', data, 4)[0] == cpu, 'Wrong Mach-O architecture'
        same_payload(data, 'darwin-' + arch)
        plist = plistlib.loads(archive.read('Wallpaper.app/Contents/Info.plist'))
        assert plist['CFBundleExecutable'] == 'wallpaperd'
        assert plist['CFBundleShortVersionString'] == version.split('-')[0]
        assert plist['CFBundleVersion'] == version.split('-')[0]
        assert plist['LSUIElement'] is True
        for name in ['LICENSE', 'RELEASE-NOTES.txt', 'BUILD-INFO.txt', 'THIRD-PARTY-NOTICES.txt']:
            assert archive.read('Wallpaper.app/Contents/Resources/' + name) == (Path('dist/package-metadata') / name).read_bytes(), 'App metadata differs: ' + name
with tarfile.open(root / (base + '-linux-amd64.tar.gz'), 'r:gz') as archive:
    prefix = base + '-linux-amd64/'
    names = [m.name for m in archive.getmembers()]
    assert set(names) == {prefix + n for n in ['wallpaperd', 'LICENSE', 'RELEASE-NOTES.txt', 'BUILD-INFO.txt', 'THIRD-PARTY-NOTICES.txt']}
    assert len(names) == len(set(names))
    for member in archive.getmembers():
        assert member.isfile() and '..' not in PurePosixPath(member.name).parts, 'Unsafe tar member'
    member = archive.getmember(prefix + 'wallpaperd')
    assert member.mode & 0o111, 'Missing Linux executable permissions'
    data = archive.extractfile(member).read()
    assert data[:4] == b'\x7fELF' and data[4:6] == bytes([2, 1]) and struct.unpack_from('<H', data, 18)[0] == 62, 'Wrong Linux architecture'
    same_payload(data, 'linux-amd64')
    for name in ['LICENSE', 'RELEASE-NOTES.txt', 'BUILD-INFO.txt', 'THIRD-PARTY-NOTICES.txt']:
        assert archive.extractfile(prefix + name).read() == (Path('dist/package-metadata') / name).read_bytes(), 'Linux metadata differs: ' + name
if sys.platform == 'linux':
    native_version = subprocess.check_output([str(Path('dist/linux-amd64/wallpaperd').resolve()), '-version'], text=True).strip()
    assert re.fullmatch(r'wallpaperd ' + re.escape(version) + r' commit=\S+ commit_time=\S+ go=\S+ linux/amd64', native_version), 'Linux runtime version mismatch: ' + native_version
print('PASS: exact asset set and checksum coverage, commit/version provenance, five payloads, architecture/permissions, metadata/notices, NSIS extraction; no bundled mpv.')
print('Not tested: installation or Windows/macOS runtime behavior; Developer ID/notarization are not established by this check.')
PY
