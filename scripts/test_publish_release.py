"""Offline release lifecycle tests. No test makes a network request or Git write.

The fake runs below the gh wrapper, so JSON decoding, CLI errors, the published-
only REST tag lookup and draft retries are exercised rather than mocked away.
"""
import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock
from urllib.parse import parse_qs, urlsplit

spec = importlib.util.spec_from_file_location('publisher', Path(__file__).with_name('publish-release.py'))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)

COMMIT = 'a' * 40
OTHER_COMMIT = 'b' * 40
NOTES = '# Native checkpoint\n\nUnsigned prerelease.\n'


class ReleaseFixture:
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.folder = self.root / 'release-assets'
        self.folder.mkdir()
        for name in ['windows-amd64-setup.exe', 'windows-arm64-setup.exe', 'macos-amd64.app.zip', 'macos-arm64.app.zip', 'linux-amd64.tar.gz']:
            (self.folder / f'Wallpaper-{publisher.VERSION}-{name}').write_bytes(b'fixture-' + name.encode())
        (self.folder / 'BUILD-INFO.txt').write_text(f'version={publisher.VERSION}\ncommit={COMMIT}\ndirty=false\n')
        (self.folder / 'RELEASE-NOTES.txt').write_text(NOTES)
        self.write_sums()
        declaration = self.root / '.github' / 'releases' / (publisher.TAG + '.json')
        declaration.parent.mkdir(parents=True)
        declaration.write_text(json.dumps({
            'version': publisher.VERSION, 'tag': publisher.TAG, 'prerelease': True,
            'notes': 'docs/releases/' + publisher.TAG + '.md',
        }))
        notes = self.root / 'docs' / 'releases' / (publisher.TAG + '.md')
        notes.parent.mkdir(parents=True)
        notes.write_text(NOTES)

    def write_sums(self):
        (self.folder / 'SHA256SUMS').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest() + '  ' + p.name + '\n' for p in sorted(self.folder.iterdir()) if p.name != 'SHA256SUMS'))


class AssetVerificationTests(ReleaseFixture, unittest.TestCase):
    def test_exact_assets(self):
        self.assertEqual(len(publisher.expected_assets(self.folder)), 8)

    def test_changed_bytes_rejected(self):
        (self.folder / 'BUILD-INFO.txt').write_text('tampered')
        with self.assertRaisesRegex(RuntimeError, 'Checksum mismatch'):
            publisher.expected_assets(self.folder)

    def test_extra_asset_rejected(self):
        (self.folder / 'unexpected').write_text('x')
        with self.assertRaisesRegex(RuntimeError, 'Unexpected release asset set'):
            publisher.expected_assets(self.folder)

    def test_missing_manifest_coverage_rejected(self):
        sums = self.folder / 'SHA256SUMS'
        sums.write_text('\n'.join(sums.read_text().splitlines()[1:]) + '\n')
        with self.assertRaisesRegex(RuntimeError, 'cover every'):
            publisher.expected_assets(self.folder)

    def test_duplicate_manifest_rejected(self):
        sums = self.folder / 'SHA256SUMS'
        sums.write_text(sums.read_text() + sums.read_text().splitlines()[0] + '\n')
        with self.assertRaisesRegex(RuntimeError, 'duplicate'):
            publisher.expected_assets(self.folder)

    def test_malformed_manifest_rejected(self):
        (self.folder / 'SHA256SUMS').write_text('not a checksum\n')
        with self.assertRaisesRegex(RuntimeError, 'Malformed'):
            publisher.expected_assets(self.folder)

    def test_remote_asset_completeness_and_hash(self):
        assets = publisher.expected_assets(self.folder)
        remote = [dict(name=n, size=a['size'], digest=a['digest'], state='uploaded') for n, a in assets.items()]
        publisher.validate_remote_assets(remote, assets, complete=True)
        with self.assertRaisesRegex(RuntimeError, 'incomplete'):
            publisher.validate_remote_assets(remote[:-1], assets, complete=True)
        remote[0]['digest'] = 'sha256:' + '0' * 64
        with self.assertRaisesRegex(RuntimeError, 'will not overwrite'):
            publisher.validate_remote_assets(remote, assets)

    def test_remote_duplicate_rejected(self):
        assets = publisher.expected_assets(self.folder)
        name, asset = next(iter(assets.items()))
        remote = dict(name=name, size=asset['size'], digest=asset['digest'], state='uploaded')
        with self.assertRaisesRegex(RuntimeError, 'duplicate'):
            publisher.validate_remote_assets([remote, remote], assets)

    def test_symlink_rejected(self):
        notes = self.folder / 'RELEASE-NOTES.txt'
        notes.unlink()
        notes.symlink_to(self.folder / 'BUILD-INFO.txt')
        with self.assertRaisesRegex(RuntimeError, 'regular asset'):
            publisher.expected_assets(self.folder)

    def test_empty_asset_rejected(self):
        (self.folder / 'RELEASE-NOTES.txt').write_bytes(b'')
        self.write_sums()
        with self.assertRaisesRegex(RuntimeError, 'nonempty'):
            publisher.expected_assets(self.folder)


class FakeGitHub:
    """Small stateful model of the REST/gh operations used by the publisher.

    REST releases/tags resolves published releases only. Authenticated release
    listing and ID lookup include drafts. All subprocess calls are intercepted.
    """
    def __init__(self, folder):
        self.folder = folder
        self.master = COMMIT
        self.tag = None
        self.releases = []
        self.calls = []
        self.writes = []
        self.uploads = []
        self.after_upload = None
        self.after_publish = None
        self.upload_failure_at = None
        self.upload_stdout = ''  # gh release upload with captured stdout is silent.

    def asset(self, name):
        path = self.folder / name
        return {'name': name, 'state': 'uploaded', 'size': path.stat().st_size,
                'digest': 'sha256:' + hashlib.sha256(path.read_bytes()).hexdigest()}

    def add_release(self, *, draft=True, assets=(), tag=None):
        release = {
            'id': len(self.releases) + 1,
            'tag_name': tag or publisher.TAG,
            'target_commitish': COMMIT,
            'name': tag or publisher.TAG,
            'body': NOTES,
            'draft': draft,
            'prerelease': True,
            'assets': [self.asset(name) for name in assets],
            'html_url': f'https://github.com/{publisher.REPOSITORY}/releases/tag/{tag or publisher.TAG}',
        }
        self.releases.append(release)
        return release

    @staticmethod
    def response(command, value=None, *, error=None, stdout=None):
        if error is not None:
            return subprocess.CompletedProcess(command, 1, '', error)
        return subprocess.CompletedProcess(command, 0, json.dumps(value) if stdout is None else stdout, '')

    def run(self, command, **kwargs):
        assert kwargs.get('capture_output') is True and kwargs.get('text') is True
        assert command[0] == 'gh', command
        self.calls.append(command[:])
        if command[1:3] == ['release', 'upload']:
            assert '--clobber' not in command, 'Existing release assets must never be overwritten'
            assert command[3] == publisher.TAG
            assert command[command.index('--repo') + 1] == publisher.REPOSITORY
            matching = [r for r in self.releases if r['tag_name'] == publisher.TAG]
            assert len(matching) == 1, 'Upload must resolve exactly one release'
            release = matching[0]
            name = Path(command[4]).name
            self.writes.append(('upload', name))
            if any(a['name'] == name for a in release['assets']):
                return self.response(command, error='asset under the same name already exists')
            release['assets'].append(self.asset(name))
            self.uploads.append(name)
            if self.after_upload:
                self.after_upload(self, release)
            # The server may have accepted an upload before the client loses its
            # response. Retrying must inspect remote bytes, not blindly upload.
            if self.upload_failure_at == len(self.uploads):
                return self.response(command, error='connection lost after upload')
            return self.response(command, stdout=self.upload_stdout)
        assert command[1] == 'api', command
        prefix = 'repos/' + publisher.REPOSITORY + '/'
        assert command[2].startswith(prefix), command
        raw_path = command[2][len(prefix):]
        parsed = urlsplit(raw_path)
        path = parsed.path
        query = {key: values[-1] for key, values in parse_qs(parsed.query).items()}
        args = command[3:]
        method = 'GET'
        fields = {}
        for index, arg in enumerate(args):
            if arg in ('-X', '--method'):
                method = args[index + 1]
            elif arg in ('-f', '-F', '--field', '--raw-field'):
                key, value = args[index + 1].split('=', 1)
                fields[key] = json.loads(value) if arg in ('-F', '--field') and value in ('true', 'false') else value
        query.update(fields if method == 'GET' else {})
        if method != 'GET':
            self.writes.append((method, path))
        if path == 'git/ref/heads/master' and method == 'GET':
            return self.response(command, {'object': {'type': 'commit', 'sha': self.master}})
        if path == 'git/ref/tags/' + publisher.TAG and method == 'GET':
            if self.tag is None:
                return self.response(command, error='gh: Not Found (HTTP 404)')
            return self.response(command, {'object': self.tag})
        if path == 'git/refs' and method == 'POST':
            assert self.tag is None, 'Must not create or move an existing tag'
            assert fields == {'ref': 'refs/tags/' + publisher.TAG, 'sha': COMMIT}
            self.tag = {'type': 'commit', 'sha': fields['sha']}
            return self.response(command, {'object': self.tag})
        if path == 'releases/tags/' + publisher.TAG and method == 'GET':
            matches = [r for r in self.releases if r['tag_name'] == publisher.TAG and not r['draft']]
            if not matches:
                return self.response(command, error='gh: Not Found (HTTP 404)')
            return self.response(command, matches[0])
        if path == 'releases' and method == 'GET':
            if '--paginate' in args:
                assert '--slurp' in args, 'Paginated arrays require --slurp for valid JSON'
                return self.response(command, [self.releases])
            per_page = int(query.get('per_page', 30))
            start = (int(query.get('page', 1)) - 1) * per_page
            return self.response(command, self.releases[start:start + per_page])
        if path == 'releases' and method == 'POST':
            if any(r['tag_name'] == publisher.TAG for r in self.releases):
                return self.response(command, error='gh: Validation Failed (HTTP 422): tag_name already_exists')
            assert fields['tag_name'] == publisher.TAG
            assert fields['target_commitish'] == COMMIT
            assert fields['draft'] is True and fields['prerelease'] is True
            release = self.add_release()
            release.update(fields)
            return self.response(command, release)
        if path.startswith('releases/') and path[9:].isdigit():
            release = next(r for r in self.releases if r['id'] == int(path[9:]))
            if method == 'PATCH':
                assert release['draft'], 'Must not modify a published release'
                assert fields.get('draft') is False and fields.get('prerelease') is True
                release.update(fields)
                if self.after_publish:
                    self.after_publish(self, release)
            else:
                assert method == 'GET', 'Release deletion is prohibited'
            return self.response(command, release)
        raise AssertionError('Unexpected GitHub operation: ' + repr(command))


class PublicationLifecycleTests(ReleaseFixture, unittest.TestCase):
    def setUp(self):
        super().setUp()
        self.server = FakeGitHub(self.folder)

    def invoke(self, commit=COMMIT):
        previous = Path.cwd()
        output = io.StringIO()
        try:
            os.chdir(self.root)
            with mock.patch.object(publisher.subprocess, 'run', side_effect=self.server.run), contextlib.redirect_stdout(output):
                publisher.publish(self.folder, commit, NOTES)
        finally:
            os.chdir(previous)
        # Stdout is a single JSON report, not gh's upload progress or status text.
        return json.loads(output.getvalue())

    def seed_tag(self):
        self.server.tag = {'type': 'commit', 'sha': COMMIT}

    def all_names(self):
        return sorted(p.name for p in self.folder.iterdir())

    def assert_not_published(self):
        self.assertNotIn(('PATCH', 'releases/1'), self.server.writes)
        self.assertTrue(all(r['draft'] for r in self.server.releases))

    def test_new_release_is_published_with_exact_tag_and_eight_hashed_assets(self):
        report = self.invoke()
        self.assertEqual(self.server.tag, {'type': 'commit', 'sha': COMMIT})
        self.assertEqual(self.server.uploads, self.all_names())
        self.assertEqual(report['tag'], publisher.TAG)
        self.assertEqual(report['commit'], COMMIT)
        self.assertEqual(report['url'], self.server.releases[0]['html_url'])
        self.assertEqual({a['name'] for a in report['assets']}, set(self.all_names()))
        self.assertTrue(all(set(a) == {'name', 'size', 'digest'} for a in report['assets']))
        self.assertFalse(self.server.releases[0]['draft'])
        self.assertTrue(self.server.releases[0]['prerelease'])

    def test_upload_status_text_does_not_break_machine_readable_report(self):
        self.server.upload_stdout = 'Successfully uploaded 1 asset\n'
        self.assertEqual(self.invoke()['commit'], COMMIT)

    def test_complete_published_release_rerun_is_read_only(self):
        first = self.invoke()
        self.server.writes.clear()
        second = self.invoke()
        self.assertEqual(first, second)
        self.assertEqual(self.server.writes, [])

    def test_existing_partial_draft_is_resumed_not_recreated(self):
        self.seed_tag()
        existing = self.all_names()[:3]
        self.server.add_release(assets=existing)
        self.invoke()
        self.assertEqual(len(self.server.releases), 1)
        self.assertEqual(self.server.uploads, self.all_names()[3:])
        self.assertNotIn(('POST', 'releases'), self.server.writes)

    def test_interrupted_upload_is_verified_before_retry(self):
        self.server.upload_failure_at = 2
        with self.assertRaisesRegex(RuntimeError, 'connection lost'):
            self.invoke()
        self.assert_not_published()
        uploaded = self.server.uploads[:]
        self.server.upload_failure_at = None
        self.invoke()
        self.assertEqual(self.server.uploads, self.all_names())
        self.assertEqual(self.server.uploads[:2], uploaded)
        self.assertEqual(len(self.server.releases), 1)

    def test_draft_discovery_covers_later_pages(self):
        self.seed_tag()
        for index in range(105):
            self.server.add_release(tag='vold-' + str(index))
        target = self.server.add_release(assets=self.all_names())
        self.invoke()
        self.assertFalse(target['draft'])
        self.assertEqual(len(self.server.releases), 106)
        self.assertEqual(self.server.uploads, [])

    def test_duplicate_draft_matches_are_rejected_without_writes(self):
        self.seed_tag()
        self.server.add_release()
        self.server.add_release()
        with self.assertRaises(RuntimeError):
            self.invoke()
        self.assertEqual(self.server.writes, [])

    def test_existing_tag_for_other_commit_is_never_moved(self):
        self.server.tag = {'type': 'commit', 'sha': OTHER_COMMIT}
        with self.assertRaisesRegex(RuntimeError, 'tag'):
            self.invoke()
        self.assertEqual(self.server.writes, [])
        self.assertEqual(self.server.tag['sha'], OTHER_COMMIT)

    def test_annotated_tag_is_not_silently_replaced(self):
        self.server.tag = {'type': 'tag', 'sha': COMMIT}
        with self.assertRaisesRegex(RuntimeError, 'tag'):
            self.invoke()
        self.assertEqual(self.server.writes, [])

    def test_stale_master_is_rejected_before_any_write(self):
        self.server.master = OTHER_COMMIT
        with self.assertRaisesRegex(RuntimeError, 'master moved'):
            self.invoke()
        self.assertEqual(self.server.writes, [])

    def test_master_moving_during_upload_leaves_a_draft(self):
        self.server.after_upload = lambda server, release: setattr(server, 'master', OTHER_COMMIT)
        with self.assertRaisesRegex(RuntimeError, 'master moved'):
            self.invoke()
        self.assert_not_published()

    def test_tag_moving_during_upload_is_caught_before_publication(self):
        self.server.after_upload = lambda server, release: setattr(server, 'tag', {'type': 'commit', 'sha': OTHER_COMMIT})
        with self.assertRaises(RuntimeError):
            self.invoke()
        self.assert_not_published()

    def test_release_metadata_changed_during_upload_is_not_published(self):
        self.server.after_upload = lambda server, release: release.update(body='Changed by another actor')
        with self.assertRaises(RuntimeError):
            self.invoke()
        self.assert_not_published()

    def test_concurrently_published_draft_is_not_modified(self):
        # Simulate another actor publishing after the last successful upload.
        def publish_externally(server, release):
            if len(server.uploads) == len(self.all_names()):
                release['draft'] = False
        self.server.after_upload = publish_externally
        try:
            self.invoke()
        except RuntimeError:
            pass  # Refusing the race or read-only verification are both safe.
        self.assertNotIn(('PATCH', 'releases/1'), self.server.writes)

    def test_final_read_rechecks_release_metadata(self):
        self.server.after_publish = lambda server, release: release.update(body='Unexpected final notes')
        with self.assertRaises(RuntimeError):
            self.invoke()

    def test_final_read_rechecks_asset_digests(self):
        self.server.after_publish = lambda server, release: release['assets'][0].update(digest='sha256:' + '0' * 64)
        with self.assertRaisesRegex(RuntimeError, 'will not overwrite'):
            self.invoke()

    def test_final_read_does_not_trust_successful_publish_response(self):
        self.server.after_publish = lambda server, release: release.update(draft=True)
        with self.assertRaises(RuntimeError):
            self.invoke()

    def test_published_release_missing_assets_is_not_repaired(self):
        self.seed_tag()
        self.server.add_release(draft=False, assets=self.all_names()[:-1])
        with self.assertRaisesRegex(RuntimeError, 'incomplete'):
            self.invoke()
        self.assertEqual(self.server.writes, [])

    def test_published_digest_mismatch_is_not_overwritten(self):
        self.seed_tag()
        release = self.server.add_release(draft=False, assets=self.all_names())
        release['assets'][0]['digest'] = 'sha256:' + '0' * 64
        with self.assertRaisesRegex(RuntimeError, 'will not overwrite'):
            self.invoke()
        self.assertEqual(self.server.writes, [])

    def test_draft_without_server_digest_fails_closed(self):
        self.seed_tag()
        release = self.server.add_release(assets=self.all_names()[:1])
        release['assets'][0]['digest'] = None
        with self.assertRaises(RuntimeError):
            self.invoke()
        self.assertEqual(self.server.writes, [])

    def test_draft_unfinished_upload_fails_closed(self):
        self.seed_tag()
        release = self.server.add_release(assets=self.all_names()[:1])
        release['assets'][0]['state'] = 'starter'
        with self.assertRaisesRegex(RuntimeError, 'will not overwrite'):
            self.invoke()
        self.assertEqual(self.server.writes, [])

    def test_metadata_mismatch_is_not_modified(self):
        self.seed_tag()
        release = self.server.add_release(draft=False, assets=self.all_names())
        release['body'] = 'Someone else owns these notes'
        with self.assertRaisesRegex(RuntimeError, 'metadata'):
            self.invoke()
        self.assertEqual(self.server.writes, [])

    def test_dirty_or_unverified_provenance_fails_before_network(self):
        # Recompute checksums so failure proves the provenance guard, not the
        # asset-integrity guard. Missing clean-state evidence must also fail.
        for dirty_line in ('dirty=true', ''):
            with self.subTest(dirty_line=dirty_line):
                (self.folder / 'BUILD-INFO.txt').write_text(
                    f'version={publisher.VERSION}\ncommit={COMMIT}\n{dirty_line}\n')
                self.write_sums()
                with self.assertRaisesRegex(RuntimeError, 'dirty checkout'):
                    self.invoke()
                self.assertEqual(self.server.calls, [])

    def test_commit_provenance_mismatch_fails_before_network(self):
        with self.assertRaisesRegex(RuntimeError, 'provenance'):
            self.invoke(commit=OTHER_COMMIT)
        self.assertEqual(self.server.calls, [])

    def test_short_commit_fails_before_network(self):
        with self.assertRaisesRegex(RuntimeError, '40-character'):
            self.invoke(commit=COMMIT[:7])
        self.assertEqual(self.server.calls, [])

    def test_declaration_change_fails_before_network(self):
        declaration = self.root / '.github/releases' / (publisher.TAG + '.json')
        value = json.loads(declaration.read_text())
        value['prerelease'] = False
        declaration.write_text(json.dumps(value))
        with self.assertRaisesRegex(RuntimeError, 'declaration'):
            self.invoke()
        self.assertEqual(self.server.calls, [])


class GitHubCLIContractTests(unittest.TestCase):
    def test_empty_noninteractive_upload_output_is_success(self):
        completed = subprocess.CompletedProcess([], 0, '', '')
        with mock.patch.object(publisher.subprocess, 'run', return_value=completed):
            self.assertIsNone(publisher.gh('release', 'upload', publisher.TAG, 'fixture.zip'))

    def test_api_json_is_decoded(self):
        completed = subprocess.CompletedProcess([], 0, '{"id": 42}\n', '')
        with mock.patch.object(publisher.subprocess, 'run', return_value=completed):
            self.assertEqual(publisher.api('releases/42'), {'id': 42})

    def test_non_json_upload_status_text_is_not_parsed(self):
        completed = subprocess.CompletedProcess([], 0, 'Successfully uploaded 1 asset\n', '')
        with mock.patch.object(publisher.subprocess, 'run', return_value=completed):
            self.assertEqual(publisher.gh('release', 'upload', publisher.TAG, 'fixture.zip', json_output=False), completed.stdout)

    def test_missing_allowed_means_only_http_404(self):
        for status in (401, 403, 422, 500):
            with self.subTest(status=status):
                completed = subprocess.CompletedProcess([], 1, '', f'gh: Request failed (HTTP {status})')
                with mock.patch.object(publisher.subprocess, 'run', return_value=completed):
                    with self.assertRaises(RuntimeError):
                        publisher.api('releases/tags/' + publisher.TAG, missing_ok=True)
        completed = subprocess.CompletedProcess([], 1, '', 'gh: Not Found (HTTP 404)')
        with mock.patch.object(publisher.subprocess, 'run', return_value=completed):
            self.assertIsNone(publisher.api('releases/tags/' + publisher.TAG, missing_ok=True))

    def test_malformed_api_response_fails_closed(self):
        completed = subprocess.CompletedProcess([], 0, 'unexpected non-JSON API output', '')
        with mock.patch.object(publisher.subprocess, 'run', return_value=completed):
            with self.assertRaises((RuntimeError, ValueError)):
                publisher.api('releases/42')


if __name__ == '__main__':
    unittest.main()
