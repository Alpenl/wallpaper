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
expected |= {'BUILD-INFO.txt', 'RELEASE-NOTES.txt', 'SHA256SUMS'}
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

def has_group_icon(data):
    """Return True when a PE carries an RT_GROUP_ICON resource (type 14)."""
    assert data[:2] == b'MZ', 'Not PE'
    offset = struct.unpack_from('<I', data, 0x3c)[0]
    assert data[offset:offset+4] == b'PE\0\0', 'Invalid PE signature'
    coff = offset + 4
    sections = struct.unpack_from('<H', data, coff+2)[0]
    optional_size = struct.unpack_from('<H', data, coff+16)[0]
    optional = coff + 20
    magic = struct.unpack_from('<H', data, optional)[0]
    directory = optional + (112 if magic == 0x20b else 96)
    rva, size = struct.unpack_from('<II', data, directory + 8*2)
    if not rva or not size:
        return False
    section_base = optional + optional_size
    def rva_to_offset(value):
        for i in range(sections):
            entry = section_base + i * 40
            virtual_size, virtual_address, raw_size, raw_pointer = struct.unpack_from(
                '<IIII', data, entry + 8)
            if virtual_address <= value < virtual_address + max(virtual_size, raw_size):
                return raw_pointer + value - virtual_address
        return None
    root = rva_to_offset(rva)
    if root is None or root + 16 > len(data):
        return False
    named, ids = struct.unpack_from('<HH', data, root + 12)
    for i in range(named + ids):
        entry = root + 16 + i * 8
        name, _ = struct.unpack_from('<II', data, entry)
        if not name & 0x80000000 and name == 14:
            return True
    return False


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
    installer_data = installer.read_bytes()
    pe(installer_data, 0x14c, unsigned)
    assert has_group_icon(installer_data), 'installer has no icon resources'
    with tempfile.TemporaryDirectory(prefix='wallpaper-nsis-check-') as temp:
        subprocess.run([extractor, 'x', '-y', '-o' + temp, str(installer)], check=True, stdout=subprocess.DEVNULL)
        extracted = Path(temp)
        candidates = list(extracted.rglob('wallpaperd.exe'))
        assert len(candidates) == 1, 'Missing/ambiguous NSIS native payload'
        data = candidates[0].read_bytes()
        pe(data, machine, unsigned)
        assert has_group_icon(data), 'wallpaperd.exe has no icon resources'
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
        assert plist['CFBundleIconFile'] == 'AppIcon.icns'
        assert plist['CFBundleShortVersionString'] == version.split('-')[0]
        assert plist['CFBundleVersion'] == version.split('-')[0]
        assert plist['LSUIElement'] is True
        assert (archive.read('Wallpaper.app/Contents/Resources/AppIcon.icns') ==
                Path('assets/icon/AppIcon.icns').read_bytes()), 'App icon differs'
        for name in ['LICENSE', 'RELEASE-NOTES.txt', 'BUILD-INFO.txt', 'THIRD-PARTY-NOTICES.txt']:
            assert archive.read('Wallpaper.app/Contents/Resources/' + name) == (Path('dist/package-metadata') / name).read_bytes(), 'App metadata differs: ' + name
print('PASS: exact asset set and checksum coverage, commit/version provenance, four payloads, architecture/permissions, metadata/notices, NSIS extraction; no bundled mpv.')
print('Not tested: installation or Windows/macOS runtime behavior; Developer ID/notarization are not established by this check.')
PY
