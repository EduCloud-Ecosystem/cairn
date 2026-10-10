#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Render the prospective template contract, or check its versioned release files.

Uses only the standard library and never imports assignment/student code.
"""
import argparse
import ast
import hashlib
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1] / 'templates' / 'clustering-v2'


def render(root):
    c = json.loads((root / 'contract.json').read_text())
    assert sum(x['max_points'] for x in c['criteria']) == c['total_points'] == 100
    ids = {x['id'] for x in c['criteria']}
    assert len(ids) == len(c['criteria'])
    assert sum(x['max_points'] for x in c['criteria'] if x['id'] != 'quality') == 95
    assert len({f['name'] for f in c['functions']}) == len(c['functions'])
    assert all(f['criterion_id'] in ids or f['criterion_id'] is None for f in c['functions'])
    starter = ['# SPDX-License-Identifier: AGPL-3.0-or-later',
               f'"""{c["version"]}: implement the required functions without changing their API."""',
               'from __future__ import annotations', 'from typing import Iterable',
               'import numpy as np', 'import pandas as pd', '']
    for f in c['functions']:
        starter += ['', f'def {f["name"]}({f["parameters"]}) -> {f["returns"]}:',
                    '    """' + f['contract'] + '"""',
                    f'    raise NotImplementedError("Implement {f["name"]}")', '']
    source = '\n'.join(starter)
    ast.parse(source)
    rubric = {'title': c['version'], 'paths': ['ds.py'], 'criteria': [
        {'id': x['id'], 'max_points': x['max_points'], 'description': x['description']}
        for x in c['criteria']]}
    table = '\n'.join(f'| {x["id"]} | {x["title"]} | {x["max_points"]} |' for x in c['criteria'])
    feedback = f'# Feedback: {c["version"]}\n\nTotal: 100 points. Functional criteria: 95; instructor-reviewed quality: 5.\n\n'
    for x in c['criteria']:
        feedback += f'## {x["id"]}: {x["title"]} ({x["max_points"]} points)\n\n{x["description"]}\n\nScore: ___ / {x["max_points"]}\n\nEvidence and feedback: ___\n\n'
    feedback += ('No relevant authored work: record unassessable/null for review, not an inferred zero. '
                 'A failed environment is not a student failure. Individual test counts are not point weights. '
                 'Apply partial credit by the published obligations. No extra automatic style or process deductions.\n')
    readme = f'''# Clustering with energy features: v2

Version: **{c['version']}**. Python {c['python']} is the tested target.
This is a prospective replacement exercise. Never use these changed requirements
to rescore earlier submissions. The bundled CSV is entirely synthetic and is not
a source for claims about actual countries or energy systems.

Implement the required functions in `ds.py`, preserving their signatures and
docstrings. Work through `clustering.ipynb` to compare models and explain your
results. The notebook is formative; the 100-point grade covers `ds.py` only.
`membership_uncertainty_mask` is an optional, unscored extension.

## Run locally

```sh
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -r requirements.txt
python -m unittest -v test_ds
```

The starter deliberately raises NotImplementedError. Its tests fail until you
implement the functions; that is expected. To include the optional exercise:

```sh
CLUSTERING_TEST_OPTIONAL=1 python -m unittest -v test_ds
```

Open `clustering.ipynb` in your Python notebook environment. Use the bundled
synthetic data initially. Your instructor may provide an OWID CSV with the same
feature columns and its own provenance; do not silently substitute datasets.
No downloads, API keys or LLM calls are part of this exercise.

## One grading scheme

| Criterion | Topic | Points |
|---|---|---:|
{table}
| **Total** | | **100** |

`rubric.json` is the machine-readable Cairn rubric; `feedback.md` uses the same
criteria. Cleaning is 4 points for duplicates, 4 for missingness and 2 for input
and index preservation. Tests expose evidence; passing a fraction of tests does
not define a fraction of the grade. Lint output is diagnostic, not another ten
points. There are no undocumented automatic deductions. Starter-only work is
unassessable/null until review, not automatically a zero. Instructor review is
required before any proposed score becomes a published grade.

## Contracts that matter

- Cleaning drops duplicates before missing values. `subset_cols=None` checks all
  columns; an empty subset does no missing-value filtering. Preserve surviving
  index labels and return an independent copy.
- Standardization uses `ddof=0`. Return raw population means/stds in column order;
  divide by one internally for a constant column, producing zero. Reject empty
  or nonfinite input. Preserve the input, index and column order.
- K-Means uses `n_init=10` and the provided random seed in both the CH search and
  clustering function. This avoids dependence on a library's changing default.
- GMM outliers use **log density**, not membership probability. First call
  `calibrate_outlier_cutoff(model, X_reference, tail_fraction=0.01)`. It returns
  the linear quantile of reference log densities. Then call
  `gmm_outlier_mask(model, X, log_density_cutoff)` with that fixed cutoff.
  Strict `<` excludes equality. Ties and distribution shifts mean the flagged
  fraction need not equal `tail_fraction`. An observation's result must not
  depend on which other observations share its evaluation batch.
- Fit the scaler and model only on training data. Use a separate reference set
  to choose the cutoff; keep evaluation data out of both steps. Country/time
  records in real data require a justified grouped or time-based split.
- Optional membership uncertainty uses maximum responsibility below
  `min_confidence=0.9`. With K components the maximum is at least 1/K. This
  measures ambiguous component assignment, which is different from low density.
- Silhouette returns `np.nan` for fewer than two clusters.

Each function's docstring in `ds.py` is part of the published contract.

## Version and provenance

These are newly authored teaching materials based on the learning objectives of
INFO-523 clustering. They contain no historical student answers or identities.
The old assignment's historical exceptions do not apply to this new version.
`manifest.json` binds every distributed file to its SHA-256 digest. Dependencies
are compatible ranges rather than a platform lock; record the actual environment
when verifying or deploying a course. Do not present local checks as course
acceptance or publish this to an active assignment without a new version.

Maintainers: from the Cairn root, run `python3 tools/clustering_template.py` after
an intentional contract change, then `python3 tools/clustering_template.py --check`.
The renderer never runs student code. Do not ship a completed solution with the
student template.
'''
    notebook = json.loads((root / 'clustering.ipynb').read_text())
    notebook['metadata']['assignment_version'] = c['version']
    intro = ''.join(notebook['cells'][0]['source'])
    intro, count = re.subn(r'Version: \S+ ', f'Version: {c["version"]}. ', intro, count=1)
    assert count == 1, 'Notebook needs a version marker'
    notebook['cells'][0]['source'] = intro.splitlines(keepends=True)
    return {'clustering.ipynb': json.dumps(notebook, indent=2) + '\n', 'ds.py': source, 'rubric.json': json.dumps(rubric, indent=2) + '\n',
            'feedback.md': feedback, 'README.md': readme}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    generated = render(ROOT)
    problems = []
    for name, text in generated.items():
        p = ROOT / name
        if args.check:
            if not p.exists() or p.read_bytes() != text.encode():
                problems.append(name)
        else:
            p.write_text(text)
    notebook = json.loads((ROOT / 'clustering.ipynb').read_text())
    for cell in notebook['cells']:
        if cell['cell_type'] == 'code':
            assert cell['execution_count'] is None and cell['outputs'] == []
            ast.parse(''.join(cell['source']))
    names = sorted(set(list(generated) + ['contract.json', 'requirements.txt',
                   '.gitignore', 'clustering.ipynb', 'test_ds.py', 'data/energy-synthetic.csv']))
    manifest = {'version': json.loads((ROOT / 'contract.json').read_text())['version'],
                'files': {n: hashlib.sha256((ROOT / n).read_bytes()).hexdigest() for n in names}}
    text = json.dumps(manifest, indent=2) + '\n'
    target = ROOT / 'manifest.json'
    if args.check:
        if not target.exists() or target.read_bytes() != text.encode():
            problems.append('manifest.json')
        if problems:
            raise SystemExit('Template drift: ' + ', '.join(problems))
        print('Clustering v2 contract, generated materials, notebook and file digests agree.')
    else:
        target.write_text(text)
        print('Rendered clustering v2 and refreshed its manifest.')


if __name__ == '__main__':
    main()
