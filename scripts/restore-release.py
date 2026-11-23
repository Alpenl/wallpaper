#!/usr/bin/env python3
"""Reuse immutable package bytes, including across dispatches/re-runs.

Do not rebuild a partially published release if its artifact has expired:
rebuilding signed/time-stamped archives would differ from uploaded assets.
Note: GitHub's GET /repos/{repo}/releases endpoint only returns draft releases
when authenticated with contents: write permission.
"""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

repo = os.environ['GITHUB_REPOSITORY']
tag = os.environ['TAG']
name = 'release-' + tag + '-' + os.environ['RELEASE_COMMIT']
check_only = '--check' in sys.argv
required = '--required' in sys.argv

pages = json.loads(subprocess.check_output(['gh', 'api', '--paginate', '--slurp',
    f'repos/{repo}/actions/artifacts?name={name}&per_page=100'], text=True))
artifacts = [a for page in pages for a in page['artifacts'] if a['name'] == name and not a['expired']]
if artifacts:
    if not check_only:
        artifact = max(artifacts, key=lambda a: a['id'])
        subprocess.run(['gh', 'run', 'download', str(artifact['workflow_run']['id']), '--repo', repo,
                        '--name', name, '--dir', 'dist/release'], check=True)
    restored = True
else:
    pages = json.loads(subprocess.check_output(['gh', 'api', '--paginate', '--slurp',
        f'repos/{repo}/releases?per_page=100'], text=True))
    releases = [r for page in pages for r in page if r['tag_name'] == tag]
    if len(releases) > 1:
        raise SystemExit('Duplicate releases; refusing to choose one')
    if releases:
        release = releases[0]
        if release.get('draft') and not release.get('assets'):
            raise SystemExit('Partial draft release exists without build artifact; refusing to rebuild')
        # Complete drafts/published releases can provide their own original bytes.
        # Partial drafts must retain the build artifact; never rebuild over them.
        spec = importlib.util.spec_from_file_location('publisher', Path(__file__).with_name('publish-release.py'))
        p = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(p)
        p.configure(os.environ['VERSION'], tag)
        if check_only:
            with tempfile.TemporaryDirectory(prefix='wallpaper-release-check-') as tmp:
                subprocess.run(['gh', 'release', 'download', tag, '--repo', repo,
                                '--dir', tmp], check=True)
                p.expected_assets(Path(tmp))
        else:
            subprocess.run(['gh', 'release', 'download', tag, '--repo', repo,
                            '--dir', 'dist/release'], check=True)
            p.expected_assets(Path('dist/release'))
        restored = True
    elif required:
        raise SystemExit('Immutable build artifact unavailable; refusing to rebuild during publication')
    else:
        restored = False

if os.environ.get('GITHUB_OUTPUT'):
    with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
        output.write('restored=' + str(restored).lower() + '\n')
        output.write('needs_build=' + str(not restored).lower() + '\n')
