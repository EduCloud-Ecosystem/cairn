# SPDX-License-Identifier: AGPL-3.0-or-later
"""Guard publication against contract drift and captured notebook output."""
import contextlib
import io
import json
import shutil
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import clustering_template as template


class ReleaseContract(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / 'template'
        shutil.copytree(template.ROOT, self.root)

    def invoke(self, check):
        with patch.object(template, 'ROOT', self.root), patch.object(sys, 'argv', ['template'] + (['--check'] if check else [])), contextlib.redirect_stdout(io.StringIO()):
            template.main()

    def test_current_release_agrees(self):
        self.invoke(True)

    def test_edited_rubric_is_not_silently_accepted(self):
        p = self.root / 'rubric.json'
        d = json.loads(p.read_text()); d['criteria'][0]['max_points'] = 100
        p.write_text(json.dumps(d))
        with self.assertRaisesRegex(SystemExit, 'rubric.json'):
            self.invoke(True)

    def test_non_generated_checks_are_hash_bound(self):
        p = self.root / 'test_ds.py'
        p.write_text(p.read_text() + '\n# changed\n')
        with self.assertRaisesRegex(SystemExit, 'manifest.json'):
            self.invoke(True)

    def test_notebook_outputs_block_publication(self):
        p = self.root / 'clustering.ipynb'
        d = json.loads(p.read_text()); cell = next(c for c in d['cells'] if c['cell_type'] == 'code')
        cell['outputs'] = [{'output_type': 'stream', 'name': 'stdout', 'text': 'private output'}]
        p.write_text(json.dumps(d))
        with self.assertRaises(AssertionError):
            self.invoke(False)

    def test_points_must_remain_one_hundred(self):
        p = self.root / 'contract.json'
        d = json.loads(p.read_text()); d['criteria'][0]['max_points'] += 10
        p.write_text(json.dumps(d))
        with self.assertRaises(AssertionError):
            self.invoke(False)

    def test_contract_change_regenerates_all_derived_materials(self):
        p = self.root / 'contract.json'
        d = json.loads(p.read_text()); d['version'] = 'educloud-clustering-v2.0.1'
        d['criteria'][0]['description'] = 'Revised CSV feedback obligation.'
        d['functions'][0]['contract'] = 'Revised CSV function contract.'
        p.write_text(json.dumps(d))
        with self.assertRaises(SystemExit):
            self.invoke(True)
        self.invoke(False)
        self.invoke(True)
        for name in ['README.md', 'ds.py', 'rubric.json', 'feedback.md', 'clustering.ipynb', 'manifest.json']:
            self.assertIn('educloud-clustering-v2.0.1', (self.root / name).read_text())
        self.assertIn('Revised CSV function contract.', (self.root / 'ds.py').read_text())
        for name in ['rubric.json', 'feedback.md']:
            self.assertIn('Revised CSV feedback obligation.', (self.root / name).read_text())


if __name__ == '__main__':
    unittest.main()
