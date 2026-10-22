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
                ('0.3.0-mvp', 'dist/release', '0', True),
                ('0.3.0-mvp/../../bad', 'dist/release', '0', False),
                ('0.3.0-mvp', 'dist', '0', False),
                ('0.3.0-mvp', '../outside', '0', False),
                ('0.3.0-mvp', 'dist/../../outside', '0', False),
                ('0.3.0-mvp', 'dist/release', '1', False),
            ]:
                env = dict(os.environ, VERSION=version, RELEASE_DIR=destination, BUNDLE_MPV=bundle)
                result = subprocess.run(['bash', '-c', 'set -euo pipefail; source "$1"; package_validate', 'bash', str(scripts / 'package-common.sh')], cwd=root, env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode == 0, ok, result.stderr)
    def test_symlink_destination_guard(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / 'outside').mkdir()
            (root / 'dist').symlink_to(root / 'outside', target_is_directory=True)
            result = subprocess.run(['bash', '-c', 'set -euo pipefail; source "$1"; package_validate', 'bash', str(scripts / 'package-common.sh')], cwd=root, env=dict(os.environ, VERSION='0.3.0-mvp', RELEASE_DIR='dist/release', BUNDLE_MPV='0'), capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
    def test_metadata_from_exact_module_sources(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / 'scripts').mkdir()
            for name in ['package-common.sh', 'package-metadata.sh']:
                shutil.copy2(scripts / name, root / 'scripts' / name)
            goroot, module = root / 'goroot', root / 'module'
            goroot.mkdir(); module.mkdir()
            (goroot / 'LICENSE').write_text('Go license fixture')
            (module / 'LICENSE').write_text('Module license fixture')
            (module / 'nested').mkdir()
            (module / 'nested' / 'NOTICE').write_text('Nested notice fixture')
            shim = root / 'bin'; shim.mkdir()
            modules = json.dumps({'Path': 'wpapp', 'Main': True}) + '\n' + json.dumps({'Path': 'example.test/native', 'Version': 'v1.2.3', 'Dir': str(module)})
            (shim / 'go').write_text('#!/usr/bin/env python3\nimport sys\na=sys.argv[1:]\nresponses={"version": "go version go1.27.3 linux/amd64", "env GOROOT": ' + repr(str(goroot)) + ', "list -m -json all": ' + repr(modules) + '}\nprint(responses[" ".join(a)])\n')
            (shim / 'git').write_text('#!/usr/bin/env python3\nimport sys\nresponses={"rev-parse HEAD": "' + 'a' * 40 + '", "status --porcelain --untracked-files=normal": ""}\nprint(responses[" ".join(sys.argv[1:])])\n')
            for path in shim.iterdir(): path.chmod(0o755)
            env = dict(os.environ, PATH=str(shim) + os.pathsep + os.environ['PATH'], VERSION='0.3.0-mvp', RELEASE_DIR='dist/release', BUNDLE_MPV='0', GITHUB_SHA='a' * 40)
            for name in ['WPAPP_SIGN_CMD', 'WPAPP_CODESIGN_ID', 'WPAPP_NOTARY_PROFILE']: env.pop(name, None)
            result = subprocess.run(['bash', 'scripts/package-metadata.sh'], cwd=root, env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            info = (root / 'dist/package-metadata/BUILD-INFO.txt').read_text().splitlines()
            self.assertIn('commit=' + 'a' * 40, info)
            self.assertIn('windows_signing=unsigned', info)
            notices = (root / 'dist/package-metadata/THIRD-PARTY-NOTICES.txt').read_text()
            for marker in ['Go license fixture', 'Module license fixture', 'Nested notice fixture', 'example.test/native@v1.2.3']:
                self.assertIn(marker, notices)
            env['GITHUB_SHA'] = 'b' * 40
            result = subprocess.run(['bash', 'scripts/package-metadata.sh'], cwd=root, env=env, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0, 'Mismatched workflow commit was accepted')
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
