# SPDX-License-Identifier: AGPL-3.0-or-later
"""educloud-clustering-v2.0.0: implement the required functions without changing their API."""
from __future__ import annotations
from typing import Iterable
import numpy as np
import pandas as pd


def load_energy_data(path: str) -> pd.DataFrame:
    """Read the given CSV with pandas.read_csv and return its DataFrame."""
    raise NotImplementedError("Implement load_energy_data")


def initial_summary(df: pd.DataFrame) -> dict[str, object]:
    """Return n_rows, n_cols, columns, numeric_columns and categorical_columns. Column lists retain input order; categorical means non-numeric. Do not mutate df."""
    raise NotImplementedError("Implement initial_summary")


def clean_energy_data(df: pd.DataFrame, subset_cols: Iterable[str] | None = None) -> pd.DataFrame:
    """Return a copy after dropping duplicate rows, then rows missing any supplied subset column. With None, use all columns. An empty subset removes no rows for missingness. Keep original surviving index labels. Do not mutate df."""
    raise NotImplementedError("Implement clean_energy_data")


def filter_year_range(df: pd.DataFrame, start_year: int, end_year: int) -> pd.DataFrame:
    """Return a copy of rows with start_year <= year <= end_year. Preserve index labels and do not mutate df. Assume a year column and start_year <= end_year."""
    raise NotImplementedError("Implement filter_year_range")


def select_clustering_features(df: pd.DataFrame, feature_cols: Iterable[str]) -> pd.DataFrame:
    """Return a copy containing exactly the requested columns in requested order, preserving rows and index. Assume columns exist. Do not mutate df."""
    raise NotImplementedError("Implement select_clustering_features")


def standardize_features(df_features: pd.DataFrame) -> tuple[pd.DataFrame, np.ndarray, np.ndarray]:
    """For nonempty numeric finite input, return (scaled, means, stds) using population standard deviation ddof=0. Means and raw stds are 1D NumPy arrays in column order. Use an internal divisor of one for zero std; constant columns become zero but returned raw std stays zero. Preserve columns/index and do not mutate input. Raise ValueError for empty rows/columns or any NaN/infinite value."""
    raise NotImplementedError("Implement standardize_features")


def compute_calinski_harabasz_scores(X: pd.DataFrame | np.ndarray, cluster_range: Iterable[int], random_state: int = 42) -> dict[int, float]:
    """For each valid k in cluster_range, fit sklearn KMeans(n_clusters=k, random_state=random_state, n_init=10) and compute sklearn.metrics.calinski_harabasz_score. Return {k: score}. Do not mutate X; assume finite input and valid k."""
    raise NotImplementedError("Implement compute_calinski_harabasz_scores")


def run_kmeans(X: pd.DataFrame | np.ndarray, n_clusters: int, random_state: int = 42) -> tuple[np.ndarray, np.ndarray]:
    """Fit sklearn KMeans(n_clusters=n_clusters, random_state=random_state, n_init=10). Return 1D labels and 2D cluster centers. Do not mutate X; assume finite valid input."""
    raise NotImplementedError("Implement run_kmeans")


def run_hierarchical_clustering(X: pd.DataFrame | np.ndarray, n_clusters: int, linkage: str = "ward") -> np.ndarray:
    """Return sklearn AgglomerativeClustering labels with the requested n_clusters and linkage. Support ward, complete and average. Do not mutate X; assume finite valid input."""
    raise NotImplementedError("Implement run_hierarchical_clustering")


def fit_gmm(X: pd.DataFrame | np.ndarray, n_components: int, random_state: int = 42) -> object:
    """Fit and return sklearn GaussianMixture(n_components=n_components, random_state=random_state). Do not mutate X; assume finite valid input."""
    raise NotImplementedError("Implement fit_gmm")


def gmm_cluster_labels(model: object, X: pd.DataFrame | np.ndarray) -> np.ndarray:
    """Return model.predict(X) as the 1D NumPy labels. Do not fit or change the model or X."""
    raise NotImplementedError("Implement gmm_cluster_labels")


def calibrate_outlier_cutoff(model: object, X_reference: pd.DataFrame | np.ndarray, tail_fraction: float = 0.01) -> float:
    """Using a fitted model and separate nonempty reference set, return the float quantile of model.score_samples(X_reference), with method="linear". Require finite tail_fraction strictly between 0 and 1 and finite 1D scores of the same length as X_reference; otherwise raise ValueError. Do not refit the model or mutate inputs. The returned value is a log-density cutoff, not a probability."""
    raise NotImplementedError("Implement calibrate_outlier_cutoff")


def gmm_outlier_mask(model: object, X: pd.DataFrame | np.ndarray, log_density_cutoff: float) -> np.ndarray:
    """Return a 1D boolean NumPy mask where model.score_samples(X) is strictly less than the supplied finite log_density_cutoff. Equality is not an outlier. Require finite 1D scores matching len(X); otherwise raise ValueError. A valid empty score array returns an empty boolean mask. Never recalibrate on this evaluation batch, refit the model or mutate X. Reject a nonfinite cutoff with ValueError."""
    raise NotImplementedError("Implement gmm_outlier_mask")


def silhouette_for_labels(X: pd.DataFrame | np.ndarray, labels: np.ndarray) -> float:
    """Return sklearn.metrics.silhouette_score(X, labels). Return np.nan when labels contain fewer than two clusters; do not raise for that case. Assume all other sklearn preconditions are satisfied. Do not mutate inputs."""
    raise NotImplementedError("Implement silhouette_for_labels")


def membership_uncertainty_mask(model: object, X: pd.DataFrame | np.ndarray, min_confidence: float = 0.9) -> np.ndarray:
    """Optional unscored exercise. Return model.predict_proba(X).max(axis=1) < min_confidence. Require finite 0 < min_confidence <= 1 or raise ValueError. Assume valid normalized responsibilities with at least one component. Return a 1D boolean NumPy mask without changing model or input. This measures uncertain component membership, not low mixture density."""
    raise NotImplementedError("Implement membership_uncertainty_mask")
