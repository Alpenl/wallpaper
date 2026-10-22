#!/usr/bin/env python3
"""Resolve an existing release tag; never create a ref."""
import importlib.util
import os
import subprocess
from pathlib import Path

spec = importlib.util.spec_from_file_location('publisher', Path(__file__).with_name('publish-release.py'))
p = importlib.util.module_from_spec(spec)
spec.loader.exec_module(p)
p.configure(tag=os.environ['TAG'])
# checkout fetch-depth: 0 supplies tags and origin/master without persisting credentials.
commit = subprocess.check_output(['git', 'rev-parse', '--verify', 'refs/tags/' + p.TAG + '^{commit}'], text=True).strip()
subprocess.run(['git', 'merge-base', '--is-ancestor', commit, 'origin/master'], check=True)
with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
    output.write(f'tag={p.TAG}\nversion={p.VERSION}\ncommit={commit}\n')
