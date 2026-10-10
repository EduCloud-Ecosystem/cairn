# Clustering with energy features: v2

Version: **educloud-clustering-v2.0.0**. Python 3.12 is the tested target.
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
| functional_1 | CSV loading | 5 |
| functional_2 | Data summary | 5 |
| functional_3 | Cleaning | 10 |
| functional_4 | Year filtering | 5 |
| functional_5 | Feature selection | 5 |
| functional_6 | Standardization | 10 |
| functional_7 | Calinski–Harabasz search | 10 |
| functional_8 | K-Means | 10 |
| functional_9 | Hierarchical clustering | 5 |
| functional_10 | GMM fitting and labels | 10 |
| functional_11 | Density outliers | 10 |
| functional_12 | Silhouette | 10 |
| quality | Code quality | 5 |
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
