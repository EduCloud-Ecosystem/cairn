# Feedback: educloud-clustering-v2.0.0

Total: 100 points. Functional criteria: 95; instructor-reviewed quality: 5.

## functional_1: CSV loading (5 points)

Read the given CSV and return its DataFrame.

Score: ___ / 5

Evidence and feedback: ___

## functional_2: Data summary (5 points)

Correct dimensions and ordered numeric/non-numeric column lists without mutation.

Score: ___ / 5

Evidence and feedback: ___

## functional_3: Cleaning (10 points)

Remove duplicates (4), implement missing-value/default/subset behavior (4), preserve input and surviving index (2).

Score: ___ / 10

Evidence and feedback: ___

## functional_4: Year filtering (5 points)

Inclusive year bounds with unchanged input and preserved surviving index.

Score: ___ / 5

Evidence and feedback: ___

## functional_5: Feature selection (5 points)

Requested column order, unchanged row/index structure, independent copy.

Score: ___ / 5

Evidence and feedback: ___

## functional_6: Standardization (10 points)

Population transform including constants and validation (6), correct raw means/stds arrays (2), preserved layout and input (2).

Score: ___ / 10

Evidence and feedback: ___

## functional_7: Calinski–Harabasz search (10 points)

Correct k-to-score mapping using seeded KMeans n_init=10 and sklearn CH, without mutation.

Score: ___ / 10

Evidence and feedback: ___

## functional_8: K-Means (10 points)

Correct labels/centers, seeded reproducibility with n_init=10, unchanged input.

Score: ___ / 10

Evidence and feedback: ___

## functional_9: Hierarchical clustering (5 points)

Requested cluster count and ward/complete/average linkage with unchanged input.

Score: ___ / 5

Evidence and feedback: ___

## functional_10: GMM fitting and labels (10 points)

Proper seeded GaussianMixture fit and predict, without mutation/refitting during prediction.

Score: ___ / 10

Evidence and feedback: ___

## functional_11: Density outliers (10 points)

Reference-set linear quantile with valid tail fraction (4); fixed log-density cutoff, strict comparison, boolean shape, finite validation and batch invariance (6).

Score: ___ / 10

Evidence and feedback: ___

## functional_12: Silhouette (10 points)

Match sklearn and return np.nan for fewer than two clusters, without mutation.

Score: ___ / 10

Evidence and feedback: ___

## quality: Code quality (5 points)

Readable modular logic and names; no hardcoding or unnecessary duplication. Instructor-reviewed. No separate style-test points.

Score: ___ / 5

Evidence and feedback: ___

No relevant authored work: record unassessable/null for review, not an inferred zero. A failed environment is not a student failure. Individual test counts are not point weights. Apply partial credit by the published obligations. No extra automatic style or process deductions.
