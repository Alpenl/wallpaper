"""Offline workflow helper contracts: tag resolution and immutable build reuse."""
import json
import os
from pathlib import Path
import runpy
import subprocess
import tempfile
import unittest
from unittest import mock

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


if __name__ == '__main__':
    unittest.main()


class RuntimeVersionTests(unittest.TestCase):
    def test_release_runtime_version_contract(self):
        # Exercise the actual assertion embedded in the release verifier.
        source = (SCRIPTS / 'release-verify.sh').read_text()
        assertion = next(line.strip() for line in source.splitlines()
                         if line.strip().startswith('assert re.fullmatch('))
        import re
        good = 'wallpaperd 1.2.3 commit=abc+dirty commit_time=unknown go=go1.27.3 linux/amd64'
        exec(assertion, dict(re=re, version='1.2.3', native_version=good))
        for bad in [good.replace('1.2.3', 'dev'), good.replace('1.2.3', '1.2.30'),
                    good.replace('linux/amd64', 'darwin/arm64'), 'wallpaperd 1.2.3 linux/amd64']:
            with self.assertRaises(AssertionError):
                exec(assertion, dict(re=re, version='1.2.3', native_version=bad))
