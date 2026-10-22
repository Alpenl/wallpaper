#!/usr/bin/env python3
"""Reuse immutable package bytes, including across dispatches/re-runs.

Do not rebuild a partially published release if its artifact has expired:
rebuilding signed/time-stamped archives would differ from uploaded assets.
"""
import json
import os
from pathlib import Path
import subprocess
import sys

repo = os.environ['GITHUB_REPOSITORY']
name = 'release-' + os.environ['TAG'] + '-' + os.environ['RELEASE_COMMIT']
pages = json.loads(subprocess.check_output(['gh', 'api', '--paginate', '--slurp',
    f'repos/{repo}/actions/artifacts?name={name}&per_page=100'], text=True))
artifacts = [a for page in pages for a in page['artifacts'] if a['name'] == name and not a['expired']]
if artifacts:
    artifact = max(artifacts, key=lambda a: a['id'])
    subprocess.run(['gh', 'run', 'download', str(artifact['workflow_run']['id']), '--repo', repo,
                    '--name', name, '--dir', 'dist/release'], check=True)
    restored = True
else:
    pages = json.loads(subprocess.check_output(['gh', 'api', '--paginate', '--slurp',
        f'repos/{repo}/releases?per_page=100'], text=True))
    releases = [r for page in pages for r in page if r['tag_name'] == os.environ['TAG']]
    if len(releases) > 1:
        raise SystemExit('Duplicate releases; refusing to choose one')
    if releases:
        subprocess.run(['gh', 'release', 'download', os.environ['TAG'], '--repo', repo,
                        '--dir', 'dist/release'], check=True)
        # Complete drafts/published releases can provide their own original bytes.
        # Partial drafts must retain the build artifact; never rebuild over them.
        import importlib.util
        spec = importlib.util.spec_from_file_location('publisher', Path(__file__).with_name('publish-release.py'))
        p = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(p)
        p.configure(os.environ['VERSION'], os.environ['TAG'])
        p.expected_assets(Path('dist/release'))
        restored = True
    elif '--required' in sys.argv:
        raise SystemExit('Immutable build artifact unavailable; refusing to rebuild during publication')
    else:
        restored = False
if os.environ.get('GITHUB_OUTPUT'):
    with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
        output.write('restored=' + str(restored).lower() + '\n')
