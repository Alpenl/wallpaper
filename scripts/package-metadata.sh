#!/usr/bin/env bash
# Record the exact native build and retain dependency license texts in packages.
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/package-common.sh
package_validate
mkdir -p dist/package-metadata
python3 - "$VERSION" <<'PY'
from pathlib import Path
import json, os, re, subprocess, sys
version = sys.argv[1]
out = Path('dist/package-metadata')
def command(*args):
    return subprocess.check_output(args, text=True).strip()
commit = command('git', 'rev-parse', 'HEAD')
if os.environ.get('GITHUB_SHA') and os.environ['GITHUB_SHA'] != commit:
    raise SystemExit('GITHUB_SHA does not match checked-out HEAD')
dirty = bool(command('git', 'status', '--porcelain', '--untracked-files=normal'))
if os.environ.get('GITHUB_SHA') and dirty:
    raise SystemExit('Refusing a dirty CI release tree: provenance must match the exact workflow commit')
go_version = command('go', 'version')
if not go_version.startswith('go version go'):
    raise SystemExit('go is not the Go compiler')
# The complete module graph includes upstream test-only modules that are not
# downloaded or linked. Collect only command dependencies, unioned across every
# published target so host-specific imports cannot omit a runtime's notices.
targets = ['windows/amd64', 'windows/arm64', 'darwin/amd64', 'darwin/arm64', 'linux/amd64']
modules_by_path, decoder = {}, json.JSONDecoder()
for target in targets:
    target_os, target_arch = target.split('/')
    env = dict(os.environ, GOOS=target_os, GOARCH=target_arch, CGO_ENABLED='0')
    packages_raw = subprocess.check_output(
        ['go', 'list', '-deps', '-json', './cmd/wallpaperd'], text=True, env=env)
    while packages_raw.strip():
        entry, end = decoder.raw_decode(packages_raw.lstrip())
        packages_raw = packages_raw.lstrip()[end:]
        module = entry.get('Module')
        if not module or module.get('Main'):
            continue
        if module.get('Replace'):
            raise SystemExit('Release metadata refuses unreviewed module replacements')
        selected = {key: module.get(key) for key in ['Path', 'Version', 'Dir']}
        if not selected['Path'] or not selected['Version']:
            raise SystemExit('Incomplete runtime module identity for ' + entry.get('ImportPath', target))
        previous = modules_by_path.setdefault(selected['Path'], selected)
        if previous != selected:
            raise SystemExit('Inconsistent runtime module metadata across targets: ' + selected['Path'])
modules = [modules_by_path[path] for path in sorted(modules_by_path)]
lines = [f'version={version}', f'commit={commit}', f'dirty={str(dirty).lower()}',
         f'go={go_version}', 'cgo_enabled=0',
         'targets=' + ' '.join(targets),
         'updater_signing=' + ('signed' if os.environ.get('WPAPP_UPDATE_KEY') and os.environ.get('WPAPP_UPDATE_PUB') else 'unsigned'),
         'mpv_bundled=false', 'ffmpeg_bundled=false', 'production_update_feed=not-configured',
         'windows_signing=' + ('requested; see external signature verification' if os.environ.get('WPAPP_SIGN_CMD') else 'unsigned'),
         'macos_developer_id=' + ('requested; see external signature verification' if os.environ.get('WPAPP_CODESIGN_ID') else 'none'),
         'macos_notarization=' + ('requested; see external notarization verification' if os.environ.get('WPAPP_NOTARY_PROFILE') else 'none'),
         'platform_runtime_testing=not-established-by-this-build']
for module in modules:
    if not module.get('Main'):
        if module.get('Replace'):
            raise SystemExit('Release metadata refuses unreviewed module replacements')
        lines.append('module=' + module['Path'] + '@' + module['Version'])
(out / 'BUILD-INFO.txt').write_text('\n'.join(lines) + '\n')
notice = ['THIRD-PARTY NOTICES', '',
          'These license texts are copied from the exact Go toolchain/module sources used for this build.',
          'They describe third-party components, not a new license grant for the Wallpaper application.',
          'mpv and ffmpeg are not included in this distribution.', '']
roots = [('Go toolchain (' + go_version + ')', Path(command('go', 'env', 'GOROOT')))]
for module in modules:
    if module.get('Main'):
        continue
    if not module.get('Dir'):
        raise SystemExit('Missing downloaded module source: ' + module['Path'])
    roots.append((module['Path'] + '@' + module['Version'], Path(module['Dir'])))
for label, root in roots:
    candidates = [p for p in root.iterdir() if p.is_file() and
                  re.match(r'^(LICENSE|LICENCE|COPYING|NOTICE|PATENTS)(?:[._-].*)?$', p.name, re.I)]
    # Additional embedded third-party notices in modules (not just their root license).
    if not label.startswith('Go toolchain'):
        candidates += [p for p in root.rglob('*') if p.is_file() and p.parent != root and
                       re.match(r'^(LICENSE|LICENCE|COPYING|NOTICE)(?:[._-].*)?$', p.name, re.I)]
    else:
        vendor = root / 'src' / 'vendor'
        if vendor.is_dir():
            candidates += [p for p in vendor.rglob('*') if p.is_file() and
                           re.match(r'^(LICENSE|LICENCE|COPYING|NOTICE)(?:[._-].*)?$', p.name, re.I)]
    if not candidates:
        raise SystemExit('Missing license text: ' + label)
    for path in sorted(set(candidates)):
        if not path.resolve().is_relative_to(root.resolve()):
            raise SystemExit('License path leaves module: ' + str(path))
        notice += ['=' * 72, label + ' / ' + path.relative_to(root).as_posix(),
                   '=' * 72, path.read_text(errors='replace').rstrip(), '']
(out / 'THIRD-PARTY-NOTICES.txt').write_text('\n'.join(notice) + '\n')
notes = f'''Wallpaper {version} native release

Native Go + MyGo application only. No React runtime is included.

SIGNING AND VALIDATION
Default artifacts have no Windows Authenticode signature and no macOS Apple
Developer ID signature/notarization. A Go-generated ad-hoc Mach-O signature is
not an identified developer signature. OS warnings are expected; do not bypass
security controls unless you have verified and trust this release. See
BUILD-INFO.txt for the build's actual signing-hook configuration. Compilation
and package verification do not establish Windows/macOS runtime acceptance,
hardware decoding, power efficiency, installation behavior, or OS compatibility.
No hosted update feed is configured. If updater keys were supplied, the separate
updates archive contains verified signed manifests for deployment by the operator.
See BUILD-INFO.txt for updater signing status. Download new releases manually.

WINDOWS amd64 / arm64
Run the matching setup.exe. Installation is per-user, without administrator
rights, under %LOCALAPPDATA%\\Programs\\Wallpaper. The NSIS installer itself is
an x86 bootstrap; its wallpaperd.exe payload matches the filename architecture.
mpv IS NOT BUNDLED: default Windows video playback needs a separately installed
matching-architecture mpv from https://mpv.io/installation/. Extract mpv's complete
upstream distribution to a user-controlled directory. In Wallpaper's Playback
& Performance page, under Decode & render, set the mpv path to that mpv.exe.
Alternatively, put mpv.exe on PATH. Restart the application after changing PATH. The experimental Media
Foundation route is not a certified replacement for this runtime dependency.
Optional Windows transcoding additionally requires the user's own ffmpeg build
with h264_mf. No codec or runtime is downloaded silently.

macOS amd64 / arm64
Unzip the matching .app.zip and move Wallpaper.app to Applications. Uses system
AVFoundation; no mpv/ffmpeg is needed for the default macOS playback/transcode
route. The app has a menu-bar presence (LSUIElement) rather than a Dock icon.
macOS may refuse to launch this unnotarized developer build; only proceed using
Apple's supported controls after verifying the source/checksum. True desktop,
mixed-DPI, sleep/wake, and hardware-encoding behavior still requires real-device
acceptance. The bundle declares macOS 12.0 as minimum, not a tested compatibility
matrix.

LINUX amd64
Extract the .tar.gz and run ./wallpaperd in a graphical X11 session. Install mpv
from the distribution's package manager for video playback; ffmpeg is optional
for Linux-compatible transcoding. This is a limited X11 compatibility build,
not complete Wayland/GNOME/KDE product support.

DATA AND CHECKSUMS
Back up your existing Wallpaper configuration/library before testing. User data
is retained by the Windows uninstaller. WPAPP_HOME can select an isolated data
directory. SHA256SUMS covers every public asset except the manifest itself.
Checksums detect corruption but are not a publisher signature. Third-party
license notices are inside each package/install directory.

Source and detailed implementation/validation boundaries:
https://github.com/Alpenl/wallpaper
'''
(out / 'RELEASE-NOTES.txt').write_text(notes)
print('Package metadata and dependency notices written.')
PY
