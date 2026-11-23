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
metadata_lines = (root / 'BUILD-INFO.txt').read_text().splitlines()
if 'updater_signing=signed' in metadata_lines:
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
assert 'version=' + version in metadata_lines, 'Version provenance mismatch'
assert 'commit=' + subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip() in metadata_lines, 'Commit provenance mismatch'
assert 'production_update_feed=not-configured' in metadata_lines
if os.environ.get('GITHUB_SHA'):
    assert 'dirty=false' in metadata_lines, 'Dirty CI release provenance'
assert Path('dist/package-metadata/LICENSE').read_bytes() == Path('LICENSE').read_bytes(), 'App license differs'
notices = Path('dist/package-metadata/THIRD-PARTY-NOTICES.txt').read_bytes()
assert notices and b'THIRD-PARTY NOTICES' in notices

win_unsigned = 'windows_signing=unsigned' in metadata_lines
macos_signed = 'macos_developer_id=none' not in metadata_lines

def pe(data, machine, unsigned=False):
    assert data[:2] == b'MZ', 'Not PE'
    offset = struct.unpack_from('<I', data, 0x3c)[0]
    assert data[offset:offset+4] == b'PE\0\0', 'Invalid PE signature'
    assert struct.unpack_from('<H', data, offset+4)[0] == machine, 'Wrong PE architecture'
    optional = offset + 24
    assert struct.unpack_from('<H', data, optional+68)[0] == 2, 'Not a Windows GUI binary'
    magic = struct.unpack_from('<H', data, optional)[0]
    directory = optional + (112 if magic == 0x20b else 96)
    cert_offset, cert_size = struct.unpack_from('<II', data, directory+8*4)
    if unsigned:
        assert (cert_offset, cert_size) == (0, 0), 'Unexpected Authenticode signature'
    else:
        assert cert_offset > 0 and cert_size > 0 and cert_offset + cert_size <= len(data), \
            'Missing or truncated Authenticode signature'

def has_macho_code_signature(data):
    if len(data) < 32 or data[:4] != b'\xcf\xfa\xed\xfe':
        return False
    ncmds = struct.unpack_from('<I', data, 16)[0]
    offset = 32
    for _ in range(ncmds):
        if offset + 8 > len(data):
            return False
        command, size = struct.unpack_from('<II', data, offset)
        if size < 8 or offset + size > len(data):
            return False
        if command == 0x1d and size >= 16:  # LC_CODE_SIGNATURE
            dataoff, datasize = struct.unpack_from('<II', data, offset + 8)
            return dataoff > 0 and datasize > 0 and dataoff + datasize <= len(data)
        offset += size
    return False

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
    root_off = rva_to_offset(rva)
    if root_off is None or root_off + 16 > len(data):
        return False
    named, ids = struct.unpack_from('<HH', data, root_off + 12)
    for i in range(named + ids):
        entry = root_off + 16 + i * 8
        name, _ = struct.unpack_from('<II', data, entry)
        if not name & 0x80000000 and name == 14:
            return True
    return False

def same_payload(data, target):
    binary = Path('dist') / target / ('wallpaperd.exe' if target.startswith('windows-') else 'wallpaperd')
    original = binary.read_bytes()
    if target.startswith('windows-'):
        if win_unsigned:
            assert data == original, target + ': packaged binary differs from build'
        else:
            arch = target.split('-', 1)[1]
            staged = Path('dist') / ('windows-packaged-' + arch) / 'wallpaperd.exe'
            if staged.is_file():
                assert data == staged.read_bytes(), target + ': packaged binary differs from signed staging'
        return
    # Darwin payload provenance: compare signature-stripped binaries on macOS even when signed.
    if sys.platform == 'darwin' and (macos_signed or data != original):
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
    elif not macos_signed:
        assert data == original, target + ': packaged binary differs from build'
    else:
        assert has_macho_code_signature(data), target + ': signed Mach-O missing LC_CODE_SIGNATURE'

extractor = shutil.which('7z') or shutil.which('7zz')
assert extractor, 'Need 7z or 7zz to independently verify NSIS payloads (Ubuntu: p7zip-full)'
windows_payloads = {}
for arch, machine in [('amd64', 0x8664), ('arm64', 0xaa64)]:
    installer = root / (base + '-windows-' + arch + '-setup.exe')
    # NSIS uses an x86 bootstrap for both native payload architectures.
    installer_data = installer.read_bytes()
    pe(installer_data, 0x14c, win_unsigned)
    assert has_group_icon(installer_data), 'installer has no icon resources'
    with tempfile.TemporaryDirectory(prefix='wallpaper-nsis-check-') as temp:
        subprocess.run([extractor, 'x', '-y', '-o' + temp, str(installer)], check=True, stdout=subprocess.DEVNULL)
        extracted = Path(temp)
        candidates = list(extracted.rglob('wallpaperd.exe'))
        assert len(candidates) == 1, 'Missing/ambiguous NSIS native payload'
        data = candidates[0].read_bytes()
        pe(data, machine, win_unsigned)
        assert has_group_icon(data), 'wallpaperd.exe has no icon resources'
        same_payload(data, 'windows-' + arch)
        windows_payloads[arch] = data
        assert not list(extracted.rglob('mpv.exe')), 'Unexpected bundled mpv'
        for name in ['LICENSE', 'RELEASE-NOTES.txt', 'BUILD-INFO.txt', 'THIRD-PARTY-NOTICES.txt']:
            matches = list(extracted.rglob(name))
            assert len(matches) == 1 and matches[0].read_bytes() == (Path('dist/package-metadata') / name).read_bytes(), 'NSIS metadata differs: ' + name

darwin_payloads = {}
darwin_files = {}
for arch, cpu in [('amd64', 0x1000007), ('arm64', 0x100000c)]:
    zip_path = root / (base + '-macos-' + arch + '.app.zip')
    with zipfile.ZipFile(zip_path) as archive:
        assert archive.testzip() is None, 'ZIP CRC failure'
        names = archive.namelist()
        assert len(names) == len(set(names)), 'Duplicate ZIP members'
        for name in names:
            assert not name.startswith('/') and '..' not in PurePosixPath(name).parts, 'Unsafe ZIP path'
        executable = 'Wallpaper.app/Contents/MacOS/wallpaperd'
        info = archive.getinfo(executable)
        assert stat.S_ISREG(info.external_attr >> 16) and info.external_attr >> 16 & 0o111, 'Missing executable permissions'
        data = archive.read(executable)
        assert data[:4] == b'\xcf\xfa\xed\xfe' and struct.unpack_from('<I', data, 4)[0] == cpu, 'Wrong Mach-O architecture'
        same_payload(data, 'darwin-' + arch)
        darwin_payloads[arch] = data
        plist_bytes = archive.read('Wallpaper.app/Contents/Info.plist')
        plist = plistlib.loads(plist_bytes)
        assert plist['CFBundleExecutable'] == 'wallpaperd'
        assert plist['CFBundleIconFile'] == 'AppIcon.icns'
        assert plist['CFBundleShortVersionString'] == version.split('-')[0]
        assert plist['CFBundleVersion'] == version
        assert plist['LSUIElement'] is True
        assert plist.get('NSScreenCaptureUsageDescription'), 'Missing NSScreenCaptureUsageDescription'
        assert (archive.read('Wallpaper.app/Contents/Resources/AppIcon.icns') ==
                Path('assets/icon/AppIcon.icns').read_bytes()), 'App icon differs'
        for name in ['LICENSE', 'RELEASE-NOTES.txt', 'BUILD-INFO.txt', 'THIRD-PARTY-NOTICES.txt']:
            assert archive.read('Wallpaper.app/Contents/Resources/' + name) == (Path('dist/package-metadata') / name).read_bytes(), 'App metadata differs: ' + name
        files = {
            'Info.plist': plist_bytes,
            'Resources/LICENSE': archive.read('Wallpaper.app/Contents/Resources/LICENSE'),
            'Resources/THIRD-PARTY-NOTICES.txt': archive.read('Wallpaper.app/Contents/Resources/THIRD-PARTY-NOTICES.txt'),
        }
        if macos_signed:
            sig_member = 'Wallpaper.app/Contents/_CodeSignature/CodeResources'
            assert sig_member in names and archive.read(sig_member), 'Missing macOS _CodeSignature/CodeResources in signed bundle'
            files['_CodeSignature/CodeResources'] = archive.read(sig_member)
        darwin_files[arch] = files
    if macos_signed and sys.platform == 'darwin':
        with tempfile.TemporaryDirectory(prefix='wallpaper-codesign-check-') as temp:
            subprocess.run(['ditto', '-x', '-k', str(zip_path), temp], check=True)
            subprocess.run(['codesign', '--verify', '--strict', str(Path(temp) / 'Wallpaper.app')], check=True)

updates_name = base + '-updates.tar.gz'
if updates_name in expected:
    with tarfile.open(root / updates_name, 'r:gz') as archive:
        members = {}
        for member in archive.getmembers():
            raw = member.name
            assert not raw.startswith('/') and '..' not in PurePosixPath(raw).parts, 'Unsafe update tar path: ' + raw
            norm = PurePosixPath(raw).as_posix()
            if norm == '.':
                continue
            if member.isfile():
                assert norm not in members, 'Duplicate update archive member: ' + norm
                members[norm] = archive.extractfile(member).read()
        for arch, machine in [('amd64', 0x8664), ('arm64', 0xaa64)]:
            prefix = f'windows-{arch}/'
            assert members.get(prefix + 'manifest.json'), prefix + 'missing manifest.json'
            assert members.get(prefix + 'manifest.json.sig'), prefix + 'missing manifest.json.sig'
            exe = members.get(prefix + 'wallpaperd.exe')
            assert exe is not None, prefix + 'missing wallpaperd.exe'
            pe(exe, machine, win_unsigned)
            assert exe == windows_payloads[arch], prefix + 'update wallpaperd.exe differs from installer payload'
            for name in ['LICENSE', 'THIRD-PARTY-NOTICES.txt']:
                assert members.get(prefix + name) == (Path('dist/package-metadata') / name).read_bytes(), prefix + 'metadata differs: ' + name
        for arch in ['amd64', 'arm64']:
            prefix = f'darwin-{arch}/'
            assert members.get(prefix + 'manifest.json'), prefix + 'missing manifest.json'
            assert members.get(prefix + 'manifest.json.sig'), prefix + 'missing manifest.json.sig'
            assert prefix + 'wallpaperd' not in members, prefix + 'flat wallpaperd layout does not match .app/Contents'
            exe = members.get(prefix + 'MacOS/wallpaperd')
            assert exe == darwin_payloads[arch], prefix + 'update MacOS/wallpaperd differs from .app.zip payload'
            for rel, expected_bytes in darwin_files[arch].items():
                assert members.get(prefix + rel) == expected_bytes, prefix + 'update bundle member differs: ' + rel

print('PASS: exact asset set and checksum coverage, commit/version provenance, four payloads, architecture/permissions, metadata/notices, NSIS extraction, signature/update integrity; no bundled mpv.')
print('Not tested: installation or Windows/macOS runtime behavior; external certificate chain/notarization require host OS verification.')
PY
