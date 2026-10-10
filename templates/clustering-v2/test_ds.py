# SPDX-License-Identifier: AGPL-3.0-or-later
"""Published v2 contract checks. Test counts are not grading weights."""
import os
import tempfile
import unittest
from pathlib import Path

import numpy as np
import pandas as pd
from sklearn.cluster import AgglomerativeClustering, KMeans
from sklearn.metrics import calinski_harabasz_score, silhouette_score
from sklearn.mixture import GaussianMixture

import ds


class Scores:
    """Controlled model double: input is the prescribed per-row log density."""
    def score_samples(self, X):
        return np.asarray(X, dtype=float).reshape(-1)


class Membership:
    def predict_proba(self, X):
        return np.asarray(X, dtype=float)


def blobs():
    rng = np.random.default_rng(13)
    return np.vstack([rng.normal(loc=c, scale=.25, size=(12, 2)) for c in [-4, 0, 5]])


class Preparation(unittest.TestCase):
    def test_functional_1_csv(self):
        df = pd.DataFrame({'country': ['A', 'B'], 'year': [2000, 2001]})
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / 'input.csv'
            df.to_csv(path, index=False)
            pd.testing.assert_frame_equal(ds.load_energy_data(str(path)), df)

    def test_functional_2_summary(self):
        df = pd.DataFrame({'value': [1., 2., 3.], 'country': ['A', 'B', 'C'], 'year': [1, 2, 3]})
        before = df.copy(deep=True)
        got = ds.initial_summary(df)
        self.assertEqual(got, {'n_rows': 3, 'n_cols': 3, 'columns': list(df.columns),
                              'numeric_columns': ['value', 'year'], 'categorical_columns': ['country']})
        pd.testing.assert_frame_equal(df, before)

    def clean(self, df, expected, subset=None):
        before = df.copy(deep=True)
        got = ds.clean_energy_data(df, subset_cols=subset)
        pd.testing.assert_frame_equal(got, expected)
        pd.testing.assert_frame_equal(df, before)
        self.assertIsNot(got, df)
        if len(got):
            got.iloc[0, 0] = 999
            pd.testing.assert_frame_equal(df, before)

    def test_functional_3_duplicates_without_missingness(self):
        df = pd.DataFrame({'a': [1., 1., 2.], 'b': [3., 3., 4.]}, index=[9, 2, 7])
        self.clean(df, df.iloc[[0, 2]])

    def test_functional_3_subset_preserves_unrelated_missingness(self):
        df = pd.DataFrame({'a': [1., np.nan, 3.], 'b': [np.nan, 2., 3.]}, index=[8, 4, 6])
        self.clean(df, df.iloc[[0, 2]], ['a'])

    def test_functional_3_multiple_subset(self):
        df = pd.DataFrame({'a': [1., np.nan, 3.], 'b': [np.nan, 2., 3.]})
        self.clean(df, df.iloc[[2]], ['a', 'b'])

    def test_functional_3_default_checks_all_columns(self):
        df = pd.DataFrame({'a': [1., np.nan, 3.], 'b': [np.nan, 2., 3.]})
        self.clean(df, df.iloc[[2]])

    def test_functional_3_empty_subset_still_deduplicates(self):
        df = pd.DataFrame({'a': [np.nan, np.nan, 3.], 'b': [1., 1., np.nan]}, index=[9, 4, 8])
        self.clean(df, df.iloc[[0, 2]], [])

    def test_functional_4_inclusive_filter(self):
        df = pd.DataFrame({'year': [1999, 2000, 2001, 2002, 2003]}, index=[1, 9, 3, 7, 4])
        before = df.copy(deep=True)
        got = ds.filter_year_range(df, 2000, 2002)
        pd.testing.assert_frame_equal(got, df.iloc[1:4])
        got.iloc[0, 0] = 99
        pd.testing.assert_frame_equal(df, before)

    def test_functional_5_order_and_copy(self):
        df = pd.DataFrame({'a': [1., 2.], 'b': [3., 4.], 'c': [5., 6.]}, index=[9, 3])
        before = df.copy(deep=True)
        got = ds.select_clustering_features(df, ['c', 'a'])
        pd.testing.assert_frame_equal(got, df[['c', 'a']])
        got.iloc[0, 0] = 99
        pd.testing.assert_frame_equal(df, before)


class Standardization(unittest.TestCase):
    def check_scaling(self, df):
        before = df.copy(deep=True)
        got, means, stds = ds.standardize_features(df)
        self.assertIsInstance(got, pd.DataFrame)
        self.assertIsInstance(means, np.ndarray)
        self.assertIsInstance(stds, np.ndarray)
        self.assertEqual(means.shape, (df.shape[1],))
        self.assertEqual(stds.shape, (df.shape[1],))
        expected_means = df.mean().to_numpy()
        expected_stds = df.std(ddof=0).to_numpy()
        np.testing.assert_allclose(means, expected_means)
        np.testing.assert_allclose(stds, expected_stds)
        expected = (df - expected_means) / np.where(expected_stds == 0, 1, expected_stds)
        pd.testing.assert_frame_equal(got, expected)
        pd.testing.assert_frame_equal(df, before)
        got.iloc[0, 0] = 999
        pd.testing.assert_frame_equal(df, before)

    def test_functional_6_population_and_layout(self):
        self.check_scaling(pd.DataFrame({'z': [2., 5., 9., -1.], 'a': [9., 1., 7., 3.]}, index=[9, 2, 6, 4]))

    def test_functional_6_constant_column(self):
        self.check_scaling(pd.DataFrame({'a': [1., 2., 3.], 'b': [7., 7., 7.]}))

    def test_functional_6_one_row(self):
        self.check_scaling(pd.DataFrame({'a': [4.], 'b': [2.]}, index=['only']))

    def test_functional_6_empty(self):
        for df in [pd.DataFrame({'a': []}), pd.DataFrame(index=[1, 2])]:
            with self.subTest(shape=df.shape), self.assertRaises(ValueError):
                ds.standardize_features(df)

    def test_functional_6_nonfinite(self):
        for bad in [np.nan, np.inf, -np.inf]:
            with self.subTest(value=bad), self.assertRaises(ValueError):
                ds.standardize_features(pd.DataFrame({'a': [1., bad]}))


class Clustering(unittest.TestCase):
    def test_functional_7_scores(self):
        X = blobs(); before = X.copy()
        for seed in [5, 42]:
            with self.subTest(seed=seed):
                got = ds.compute_calinski_harabasz_scores(X, (k for k in [2, 3, 4]), random_state=seed)
                self.assertEqual(set(got), {2, 3, 4})
                for k in got:
                    labels = KMeans(n_clusters=k, random_state=seed, n_init=10).fit_predict(X)
                    self.assertAlmostEqual(got[k], calinski_harabasz_score(X, labels), places=6)
        np.testing.assert_array_equal(X, before)

    def test_functional_8_labels_centers_seed(self):
        X = blobs(); before = X.copy()
        for seed in [5, 42]:
            with self.subTest(seed=seed):
                labels, centers = ds.run_kmeans(X, 3, random_state=seed)
                ref = KMeans(n_clusters=3, random_state=seed, n_init=10).fit(X)
                self.assertIsInstance(labels, np.ndarray)
                self.assertIsInstance(centers, np.ndarray)
                np.testing.assert_array_equal(labels, ref.labels_)
                np.testing.assert_allclose(centers, ref.cluster_centers_)
        np.testing.assert_array_equal(X, before)

    def test_functional_9_linkage(self):
        X = blobs(); before = X.copy()
        for linkage in ['ward', 'average', 'complete']:
            with self.subTest(linkage=linkage):
                got = ds.run_hierarchical_clustering(X, 3, linkage=linkage)
                self.assertIsInstance(got, np.ndarray)
                self.assertEqual(got.shape, (len(X),))
                ref = AgglomerativeClustering(n_clusters=3, linkage=linkage).fit_predict(X)
                np.testing.assert_array_equal(got[:, None] == got[None, :], ref[:, None] == ref[None, :])
        np.testing.assert_array_equal(X, before)

    def test_functional_10_fit_predict(self):
        X = blobs(); before = X.copy()
        for seed in [5, 42]:
            with self.subTest(seed=seed):
                model = ds.fit_gmm(X, 3, random_state=seed)
                ref = GaussianMixture(n_components=3, random_state=seed).fit(X)
                np.testing.assert_allclose(model.means_, ref.means_)
                means_before = model.means_.copy()
                labels = ds.gmm_cluster_labels(model, X[:7])
                self.assertIsInstance(labels, np.ndarray)
                np.testing.assert_array_equal(labels, model.predict(X[:7]))
                np.testing.assert_array_equal(model.means_, means_before)
        np.testing.assert_array_equal(X, before)

    def test_functional_12_silhouette(self):
        X = blobs(); before = X.copy(); labels = np.repeat([0, 1, 2], 12); old_labels = labels.copy()
        self.assertAlmostEqual(ds.silhouette_for_labels(X, labels), silhouette_score(X, labels))
        np.testing.assert_array_equal(X, before)
        np.testing.assert_array_equal(labels, old_labels)

    def test_functional_12_one_cluster(self):
        self.assertTrue(np.isnan(ds.silhouette_for_labels(blobs(), np.zeros(36, dtype=int))))


class DensityOutliers(unittest.TestCase):
    def test_functional_11_reference_linear_quantile(self):
        X = np.array([[-9.], [-5.], [-2.], [1.]]); before = X.copy()
        self.assertAlmostEqual(ds.calibrate_outlier_cutoff(Scores(), X, .25), -6.)
        self.assertAlmostEqual(ds.calibrate_outlier_cutoff(Scores(), X), -8.88)
        np.testing.assert_array_equal(X, before)

    def test_functional_11_reference_fraction_validation(self):
        for bad in [0., 1., -.1, 1.1, np.nan, np.inf]:
            with self.subTest(fraction=bad), self.assertRaises(ValueError):
                ds.calibrate_outlier_cutoff(Scores(), [[-2.], [-1.]], bad)

    def test_functional_11_reference_empty(self):
        with self.assertRaises(ValueError):
            ds.calibrate_outlier_cutoff(Scores(), np.empty((0, 1)))

    def test_functional_11_strict_cutoff_and_shape(self):
        for X in [np.array([[-7.], [-3.], [1.]]), pd.DataFrame({'score': [-7., -3., 1.]})]:
            before = np.asarray(X).copy()
            got = ds.gmm_outlier_mask(Scores(), X, log_density_cutoff=-3.)
            self.assertIsInstance(got, np.ndarray)
            self.assertEqual(got.dtype, bool)
            np.testing.assert_array_equal(got, [True, False, False])
            np.testing.assert_array_equal(np.asarray(X), before)

    def test_functional_11_batch_invariance(self):
        one = ds.gmm_outlier_mask(Scores(), [[-4.]], log_density_cutoff=-3.)
        mixed = ds.gmm_outlier_mask(Scores(), [[-4.], [-100.], [5.]], log_density_cutoff=-3.)
        np.testing.assert_array_equal(one, [True])
        np.testing.assert_array_equal(mixed, [True, True, False])

    def test_functional_11_tied_reference(self):
        cutoff = ds.calibrate_outlier_cutoff(Scores(), [[-2.], [-2.], [-2.]], .25)
        self.assertEqual(cutoff, -2.)
        np.testing.assert_array_equal(ds.gmm_outlier_mask(Scores(), [[-2.], [-3.]], cutoff), [False, True])

    def test_functional_11_density_not_membership(self):
        class Confident(Scores):
            def predict_proba(self, X):
                return np.tile([.99, .01], (len(X), 1))
        np.testing.assert_array_equal(ds.gmm_outlier_mask(Confident(), [[-100.], [1.]], -5.), [True, False])

    def test_functional_11_empty_evaluation(self):
        got = ds.gmm_outlier_mask(Scores(), np.empty((0, 1)), -3.)
        self.assertIsInstance(got, np.ndarray)
        self.assertEqual(got.shape, (0,))
        self.assertEqual(got.dtype, bool)

    def test_functional_11_cutoff_validation(self):
        for bad in [np.nan, np.inf, -np.inf]:
            with self.subTest(cutoff=bad), self.assertRaises(ValueError):
                ds.gmm_outlier_mask(Scores(), [[-3.]], bad)

    def test_functional_11_bad_model_scores(self):
        for values in [np.array([np.nan, 1.]), np.array([np.inf, 1.]), np.array([1.]), np.array([[1.], [2.]])]:
            class Invalid:
                def score_samples(self, X):
                    return values
            with self.subTest(values=values):
                with self.assertRaises(ValueError):
                    ds.calibrate_outlier_cutoff(Invalid(), [[1.], [2.]])
                with self.assertRaises(ValueError):
                    ds.gmm_outlier_mask(Invalid(), [[1.], [2.]], -3.)


@unittest.skipUnless(os.environ.get('CLUSTERING_TEST_OPTIONAL') == '1', 'Optional unscored extension')
class OptionalMembership(unittest.TestCase):
    def test_optional_membership(self):
        X = np.array([[.99, .01], [.6, .4], [.9, .1]]); before = X.copy()
        got = ds.membership_uncertainty_mask(Membership(), X)
        self.assertIsInstance(got, np.ndarray)
        self.assertEqual(got.dtype, bool)
        np.testing.assert_array_equal(got, [False, True, False])
        np.testing.assert_array_equal(X, before)

    def test_optional_threshold_validation(self):
        for bad in [0., -.1, 1.1, np.nan, np.inf]:
            with self.subTest(threshold=bad), self.assertRaises(ValueError):
                ds.membership_uncertainty_mask(Membership(), [[.5, .5]], bad)


if __name__ == '__main__':
    unittest.main()
