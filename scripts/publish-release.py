#!/usr/bin/env python3
"""Publish a verified tagged release without moving tags or overwriting assets."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys

REPOSITORY = os.environ.get('GITHUB_REPOSITORY', 'Alpenl/wallpaper')
VERSION = None
TAG = None


def configure(version=None, tag=None):
    global VERSION, TAG
    version = version or (tag[1:] if tag and tag.startswith('v') else None)
    tag = tag or ('v' + version if version else None)
    if not version or not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?', version):
        raise RuntimeError('Expected semver MAJOR.MINOR.PATCH with optional prerelease suffix')
    if '-' in version:
        for part in version.split('-', 1)[1].split('.'):
            if part.isdigit() and len(part) > 1 and part.startswith('0'):
                raise RuntimeError('Numeric semver prerelease identifiers cannot have leading zeros')
    if tag != 'v' + version:
        raise RuntimeError('Version and tag do not match')
    VERSION, TAG = version, tag


def verify_master(commit):
    comparison = api(f'compare/{commit}...master')
    if comparison['status'] not in ('ahead', 'identical'):
        raise RuntimeError('Tagged commit is not reachable from master')


def release_notes(commit, folder):
    path = Path('docs/releases') / (TAG + '.md')
    if path.is_file():
        notes = path.read_text()
    else:
        previous = subprocess.run(['git', 'describe', '--tags', '--abbrev=0', '--match', 'v*', commit + '^'], capture_output=True, text=True)
        revision = previous.stdout.strip() + '..' + commit if previous.returncode == 0 else commit
        notes = subprocess.check_output(['git', 'log', '--format=- %s (%h)', revision], text=True)
    unsigned = packages_unsigned(folder)
    notes += '\n\n## Signing\n' + ('Unsigned packages: Windows and/or macOS signing credentials were unavailable. This is a prerelease.\n' if unsigned else 'Windows Authenticode and macOS Developer ID signing hooks completed.\n')
    notes += 'See BUILD-INFO.txt for signing, notarization and updater-key status. No hosted update feed is configured.\n'
    return notes


def packages_unsigned(folder):
    provenance = (folder / 'BUILD-INFO.txt').read_text().splitlines()
    return not all(marker in provenance for marker in (
        'windows_signing=requested; see external signature verification',
        'macos_developer_id=requested; see external signature verification'))


def is_prerelease(folder):
    return '-' in VERSION or packages_unsigned(folder)


def gh(*args, missing_ok=False, json_output=True):
    result = subprocess.run(['gh', *args], capture_output=True, text=True)
    if result.returncode:
        if missing_ok and '(HTTP 404)' in result.stderr:
            return None
        raise RuntimeError(result.stderr.strip() or 'GitHub CLI command failed')
    if not json_output:
        return result.stdout
    return json.loads(result.stdout) if result.stdout.strip() else None


def api(path, *args, missing_ok=False):
    return gh('api', f'repos/{REPOSITORY}/{path}', *args, missing_ok=missing_ok)


def find_release():
    # The tag endpoint excludes drafts. Paginate the authorized release list so
    # an interrupted upload resumes the existing draft rather than creating one.
    release = api('releases/tags/' + TAG, missing_ok=True)
    if release is not None:
        return release
    matches = []
    for page in range(1, 101):
        releases = api(f'releases?per_page=100&page={page}')
        matches.extend(r for r in releases if r['tag_name'] == TAG)
        if len(releases) < 100:
            break
    else:
        raise RuntimeError('Release listing exceeded safe pagination limit')
    if len(matches) > 1:
        raise RuntimeError('Duplicate releases for tag; refusing to choose one')
    return matches[0] if matches else None


def verify_tag(commit):
    obj = api('git/ref/tags/' + TAG)['object']
    for _ in range(10):
        if obj['type'] != 'tag':
            break
        obj = api('git/tags/' + obj['sha'])['object']
    if obj['type'] != 'commit' or obj['sha'] != commit:
        raise RuntimeError('Existing tag differs; will not overwrite or move it')


def verify_release(release, commit, notes, prerelease):
    if release['tag_name'] != TAG or release['prerelease'] != prerelease or release['body'] != notes or release['name'] != TAG or release['target_commitish'] != commit:
        raise RuntimeError('Existing release metadata differs; refusing to modify it')


def expected_assets(folder):
    names = {
        f'Wallpaper-{VERSION}-windows-amd64-setup.exe',
        f'Wallpaper-{VERSION}-windows-arm64-setup.exe',
        f'Wallpaper-{VERSION}-macos-amd64.app.zip',
        f'Wallpaper-{VERSION}-macos-arm64.app.zip',
        f'Wallpaper-{VERSION}-linux-amd64.tar.gz',
        'BUILD-INFO.txt', 'RELEASE-NOTES.txt', 'SHA256SUMS',
    }
    if 'updater_signing=signed' in (folder / 'BUILD-INFO.txt').read_text().splitlines():
        names.add(f'Wallpaper-{VERSION}-updates.tar.gz')
    actual = {p.name for p in folder.iterdir()}
    if actual != names:
        raise RuntimeError(f'Unexpected release asset set: missing={names-actual}, extra={actual-names}')
    assets = {}
    for name in sorted(names):
        path = folder / name
        if not path.is_file() or path.is_symlink() or path.stat().st_size == 0:
            raise RuntimeError(f'Not a nonempty regular asset: {name}')
        assets[name] = {'path': path, 'size': path.stat().st_size,
                        'digest': 'sha256:' + hashlib.sha256(path.read_bytes()).hexdigest()}
    sums = {}
    for line in (folder / 'SHA256SUMS').read_text().splitlines():
        match = re.fullmatch(r'([0-9a-f]{64})  ([A-Za-z0-9_.-]+)', line)
        if not match or match[2] in sums:
            raise RuntimeError('Malformed or duplicate checksum record')
        sums[match[2]] = 'sha256:' + match[1]
    if set(sums) != names - {'SHA256SUMS'}:
        raise RuntimeError('Checksum manifest must cover every non-manifest asset exactly once')
    for name, digest in sums.items():
        if assets[name]['digest'] != digest:
            raise RuntimeError(f'Checksum mismatch: {name}')
    return assets


def validate_remote_assets(remote, local, complete=False):
    seen = set()
    for asset in remote:
        name = asset['name']
        if name in seen or name not in local:
            raise RuntimeError(f'Unexpected or duplicate remote asset: {name}')
        seen.add(name)
        expected = local[name]
        if asset.get('state') != 'uploaded' or asset.get('size') != expected['size'] or asset.get('digest') != expected['digest']:
            raise RuntimeError(f'Remote asset differs; will not overwrite: {name}')
    if complete and seen != set(local):
        raise RuntimeError('Published release asset set is incomplete')
    return seen


def publish(folder, commit, notes):
    if not re.fullmatch(r'[0-9a-f]{40}', commit):
        raise RuntimeError('Expected an exact 40-character commit SHA')
    local = expected_assets(folder)
    provenance = (folder / 'BUILD-INFO.txt').read_text().splitlines()
    if 'dirty=false' not in provenance:
        raise RuntimeError('Refusing release assets from a dirty checkout')
    if f'version={VERSION}' not in provenance:
        raise RuntimeError('Build provenance version mismatch')
    if f'commit={commit}' not in provenance:
        raise RuntimeError('Build provenance does not match the exact workflow commit')
    verify_master(commit)
    verify_tag(commit)
    prerelease = is_prerelease(folder)
    release = find_release()
    if release is None:
        release = api('releases', '-X', 'POST', '-f', 'tag_name=' + TAG,
                      '-f', 'target_commitish=' + commit, '-f', 'name=' + TAG,
                      '-f', 'body=' + notes, '-F', 'draft=true', '-F', 'prerelease=' + str(prerelease).lower())
    verify_release(release, commit, notes, prerelease)
    existing = validate_remote_assets(release['assets'], local, complete=not release['draft'])
    if release['draft']:
        for name in sorted(set(local) - existing):
            # No --clobber: interruption/races can only fail, never overwrite.
            gh('release', 'upload', TAG, str(local[name]['path']), '--repo', REPOSITORY, json_output=False)
        release = api(f'releases/{release["id"]}')
        if not release['draft']:
            raise RuntimeError('Release is no longer a draft; refusing to modify published metadata')
        verify_release(release, commit, notes, prerelease)
        verify_tag(commit)
        validate_remote_assets(release['assets'], local, complete=True)
        verify_master(commit)
        release = api(f'releases/{release["id"]}', '-X', 'PATCH', '-F', 'draft=false', '-F', 'prerelease=' + str(prerelease).lower())
    # Independently read the final state. A successful edit alone is not proof.
    release = api('releases/tags/' + TAG)
    verify_release(release, commit, notes, prerelease)
    verify_tag(commit)
    if release['draft']:
        raise RuntimeError('Final publication state mismatch')
    validate_remote_assets(release['assets'], local, complete=True)
    print(json.dumps({'url': release['html_url'], 'tag': TAG, 'commit': commit,
                      'assets': [{k: a[k] for k in ('name', 'size', 'digest')} for a in release['assets']]}, indent=2))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--assets', type=Path, required=True)
    parser.add_argument('--commit', required=True)
    parser.add_argument('--version', default=os.environ.get('VERSION'))
    parser.add_argument('--tag', default=os.environ.get('TAG'))
    args = parser.parse_args()
    try:
        configure(args.version, args.tag)
        publish(args.assets, args.commit, release_notes(args.commit, args.assets))
    except (RuntimeError, OSError, KeyError, ValueError) as error:
        print(f'Release stopped safely: {error}', file=sys.stderr)
        sys.exit(1)
