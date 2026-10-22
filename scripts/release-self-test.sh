#!/usr/bin/env bash
# Toolchain-independent packaging guard tests. This does not build NSIS or Go.
set -euo pipefail
cd "$(dirname "$0")/.."
for script in scripts/package*.sh scripts/release*.sh; do bash -n "$script"; done
python3 - <<'PY'
from pathlib import Path
import json, os, re, shutil, subprocess, tempfile, unittest
scripts = Path('scripts').resolve()
class PackagingGuards(unittest.TestCase):
    def test_embedded_python(self):
        for path in [*scripts.glob('package*.sh'), *scripts.glob('release*.sh')]:
            for n, code in enumerate(re.findall(r"<<'PY'\n(.*?)\nPY", path.read_text(), re.S)):
                compile(code, str(path) + ':' + str(n), 'exec')
    def test_version_and_destination_guards(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            for version, destination, bundle, ok in [
                ('1.2.3-rc.1', 'dist/release', '0', True),
                ('1.2.3-rc.1/../../bad', 'dist/release', '0', False),
                ('1.2.3-rc.1', 'dist', '0', False),
                ('1.2.3-rc.1', '../outside', '0', False),
                ('1.2.3-rc.1', 'dist/../../outside', '0', False),
                ('1.2.3-rc.1', 'dist/release', '1', False),
            ]:
                env = dict(os.environ, VERSION=version, RELEASE_DIR=destination, BUNDLE_MPV=bundle)
                result = subprocess.run(['bash', '-c', 'set -euo pipefail; source "$1"; package_validate', 'bash', str(scripts / 'package-common.sh')], cwd=root, env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode == 0, ok, result.stderr)
    def test_symlink_destination_guard(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / 'outside').mkdir()
            (root / 'dist').symlink_to(root / 'outside', target_is_directory=True)
            result = subprocess.run(['bash', '-c', 'set -euo pipefail; source "$1"; package_validate', 'bash', str(scripts / 'package-common.sh')], cwd=root, env=dict(os.environ, VERSION='1.2.3-rc.1', RELEASE_DIR='dist/release', BUNDLE_MPV='0'), capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
    def test_metadata_from_exact_module_sources(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / 'scripts').mkdir()
            for name in ['package-common.sh', 'package-metadata.sh']:
                shutil.copy2(scripts / name, root / 'scripts' / name)
            shutil.copy2(scripts.parent / 'LICENSE', root / 'LICENSE')
            goroot = root / 'goroot'; goroot.mkdir()
            (goroot / 'LICENSE').write_text('Go license fixture')
            runtime_modules = []
            for name in ['native', 'windows-native', 'darwin-arm-native']:
                module = root / name; module.mkdir()
                (module / 'LICENSE').write_text(name + ' license fixture')
                runtime_modules.append({'Path': 'example.test/' + name, 'Version': 'v1.2.3', 'Dir': str(module)})
            (root / 'native' / 'nested').mkdir()
            (root / 'native' / 'nested' / 'NOTICE').write_text('Nested notice fixture')
            shim = root / 'bin'; shim.mkdir()
            (shim / 'fixtures.json').write_text(json.dumps({'goroot': str(goroot), 'modules': runtime_modules}))
            (shim / 'go').write_text("""#!/usr/bin/env python3
from pathlib import Path
import json, os, sys
root = Path(__file__).parent
fixtures = json.loads((root / 'fixtures.json').read_text())
args = sys.argv[1:]
if args == ['version']:
    print('go version go1.27.3 linux/amd64')
elif args == ['env', 'GOROOT']:
    print(fixtures['goroot'])
elif args == ['list', '-m', '-json', 'all']:
    # Regression fixture: an upstream test-only graph module has no source Dir.
    for module in fixtures['modules'] + [{'Path': 'example.test/test-only', 'Version': 'v9.9.9'}]:
        print(json.dumps(module))
elif args == ['list', '-deps', '-json', './cmd/wallpaperd']:
    target = os.environ['GOOS'] + '/' + os.environ['GOARCH']
    assert os.environ['CGO_ENABLED'] == '0'
    with (root / 'targets.log').open('a') as log: log.write(target + '\\n')
    modules = [dict(fixtures['modules'][0])]
    if target.startswith('windows/'): modules.append(fixtures['modules'][1])
    if target == 'darwin/arm64': modules.append(fixtures['modules'][2])
    mode = os.environ.get('FIXTURE_MODULE_MODE')
    if mode == 'missing': modules[0].pop('Dir')
    if mode == 'replacement': modules[0]['Replace'] = {'Path': './replacement'}
    if mode == 'inconsistent' and target == 'darwin/arm64': modules[0]['Version'] = 'v1.2.4'
    print(json.dumps({'ImportPath': 'fmt', 'Standard': True}))
    print(json.dumps({'ImportPath': 'wpapp/cmd/wallpaperd', 'Module': {'Path': 'wpapp', 'Main': True}}))
    for module in modules:
        # Several packages in the same module must yield only one notice set.
        for suffix in ['one', 'two']:
            print(json.dumps({'ImportPath': module['Path'] + '/' + suffix, 'Module': module}))
else:
    raise SystemExit('Unexpected fixture Go invocation: ' + repr(args))
""")
            (shim / 'git').write_text('#!/usr/bin/env python3\nimport os, sys\nresponses={"rev-parse HEAD": "' + 'a' * 40 + '", "status --porcelain --untracked-files=normal": " M dirty.go" if os.environ.get("FIXTURE_DIRTY") else ""}\nprint(responses[" ".join(sys.argv[1:])])\n')
            for name in ['go', 'git']: (shim / name).chmod(0o755)
            env = dict(os.environ, PATH=str(shim) + os.pathsep + os.environ['PATH'], VERSION='1.2.3-rc.1', RELEASE_DIR='dist/release', BUNDLE_MPV='0', GITHUB_SHA='a' * 40, GITHUB_ACTIONS='true', CI='true')
            for name in ['WPAPP_SIGN_CMD', 'WPAPP_CODESIGN_ID', 'WPAPP_NOTARY_PROFILE', 'FIXTURE_MODULE_MODE', 'FIXTURE_DIRTY']: env.pop(name, None)
            result = subprocess.run(['bash', 'scripts/package-metadata.sh'], cwd=root, env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / 'dist/package-metadata/LICENSE').read_bytes(), (root / 'LICENSE').read_bytes())
            info = (root / 'dist/package-metadata/BUILD-INFO.txt').read_text().splitlines()
            self.assertIn('commit=' + 'a' * 40, info)
            self.assertIn('windows_signing=unsigned', info)
            self.assertIn('dirty=false', info)
            self.assertEqual(sum(line.startswith('module=') for line in info), 3)
            self.assertEqual((shim / 'targets.log').read_text().splitlines(), ['windows/amd64', 'windows/arm64', 'darwin/amd64', 'darwin/arm64', 'linux/amd64'])
            notices = (root / 'dist/package-metadata/THIRD-PARTY-NOTICES.txt').read_text()
            for marker in ['Go license fixture', 'native license fixture', 'windows-native license fixture', 'darwin-arm-native license fixture', 'Nested notice fixture']:
                self.assertIn(marker, notices)
            self.assertEqual(notices.count('example.test/native@v1.2.3 / LICENSE'), 1)
            self.assertNotIn('test-only', notices)
            for changes, error in [
                ({'GITHUB_SHA': 'b' * 40}, 'GITHUB_SHA does not match'),
                ({'FIXTURE_DIRTY': '1'}, 'dirty CI release tree'),
                ({'FIXTURE_MODULE_MODE': 'missing'}, 'Missing downloaded module source'),
                ({'FIXTURE_MODULE_MODE': 'replacement'}, 'unreviewed module replacements'),
                ({'FIXTURE_MODULE_MODE': 'inconsistent'}, 'Inconsistent runtime module metadata'),
            ]:
                with self.subTest(changes=changes):
                    result = subprocess.run(['bash', 'scripts/package-metadata.sh'], cwd=root, env=dict(env, **changes), capture_output=True, text=True)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn(error, result.stderr)
    def test_no_implicit_runtime_download(self):
        release = (scripts / 'release.sh').read_text()
        windows = (scripts / 'package-windows.sh').read_text()
        self.assertNotIn('scripts/fetch-mpv.sh', release)
        self.assertNotIn('cp third_party', windows)
        self.assertIn('package_check_binary', windows)
        self.assertIn('package_check_binary', (scripts / 'package-macos.sh').read_text())
        self.assertIn('package_check_binary', (scripts / 'package-linux.sh').read_text())
unittest.main(argv=['packaging-guards'], verbosity=2)
PY
