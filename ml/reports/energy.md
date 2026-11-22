# energy model — energy-20261119.0836-68694e

District electricity demand 1–24 hours ahead. Output: MW.

> Trained and evaluated on synthetic Nexus City simulation data. Scores measure how well the model learned the simulator, not real-world forecasting skill.

## Data

- Training seeds [101, 102, 103] — days (0, 20) train, (20, 24) validation (early stopping), (24, 28) test.
- Holdout seed [104]: a different city layout never seen in training.
- Rows whose target falls into a later split are dropped (embargo). The live city (seed 42) is never used.
- Rows: {'0': {'train': 403920, 'val': 72144, 'test': 72144, 'holdout': 93456}}

## Test results

| Horizon | XGBoost MAE | seasonal_naive_24h MAE | seasonal_naive_7d MAE | XGBoost RMSE | Improvement vs best baseline |
|---|---|---|---|---|---|
| 1 h | 1.258 | 3.235 | 2.671 | 1.964 | +52.9% (seasonal_naive_7d) |
| 2 h | 1.240 | 3.245 | 2.676 | 1.961 | +53.7% (seasonal_naive_7d) |
| 3 h | 1.250 | 3.255 | 2.684 | 1.981 | +53.4% (seasonal_naive_7d) |
| 4 h | 1.277 | 3.262 | 2.686 | 2.016 | +52.5% (seasonal_naive_7d) |
| 5 h | 1.298 | 3.269 | 2.690 | 2.035 | +51.7% (seasonal_naive_7d) |
| 6 h | 1.323 | 3.275 | 2.688 | 2.060 | +50.8% (seasonal_naive_7d) |
| 7 h | 1.349 | 3.281 | 2.690 | 2.091 | +49.8% (seasonal_naive_7d) |
| 8 h | 1.369 | 3.280 | 2.680 | 2.101 | +48.9% (seasonal_naive_7d) |
| 9 h | 1.404 | 3.278 | 2.672 | 2.145 | +47.5% (seasonal_naive_7d) |
| 10 h | 1.433 | 3.279 | 2.672 | 2.181 | +46.3% (seasonal_naive_7d) |
| 11 h | 1.467 | 3.287 | 2.669 | 2.207 | +45.0% (seasonal_naive_7d) |
| 12 h | 1.481 | 3.293 | 2.671 | 2.225 | +44.5% (seasonal_naive_7d) |
| 13 h | 1.494 | 3.293 | 2.660 | 2.255 | +43.8% (seasonal_naive_7d) |
| 14 h | 1.513 | 3.299 | 2.657 | 2.273 | +43.0% (seasonal_naive_7d) |
| 15 h | 1.534 | 3.303 | 2.658 | 2.303 | +42.3% (seasonal_naive_7d) |
| 16 h | 1.539 | 3.300 | 2.657 | 2.313 | +42.1% (seasonal_naive_7d) |
| 17 h | 1.556 | 3.300 | 2.655 | 2.345 | +41.4% (seasonal_naive_7d) |
| 18 h | 1.578 | 3.316 | 2.651 | 2.370 | +40.5% (seasonal_naive_7d) |
| 19 h | 1.609 | 3.341 | 2.658 | 2.402 | +39.5% (seasonal_naive_7d) |
| 20 h | 1.628 | 3.371 | 2.670 | 2.429 | +39.0% (seasonal_naive_7d) |
| 21 h | 1.649 | 3.404 | 2.684 | 2.456 | +38.6% (seasonal_naive_7d) |
| 22 h | 1.660 | 3.436 | 2.701 | 2.484 | +38.5% (seasonal_naive_7d) |
| 23 h | 1.676 | 3.466 | 2.718 | 2.509 | +38.4% (seasonal_naive_7d) |
| 24 h | 1.696 | 3.499 | 2.730 | 2.543 | +37.9% (seasonal_naive_7d) |

## Holdout results

| Horizon | XGBoost MAE | seasonal_naive_24h MAE | seasonal_naive_7d MAE | XGBoost RMSE | Improvement vs best baseline |
|---|---|---|---|---|---|
| 1 h | 1.352 | 2.979 | 2.757 | 2.329 | +51.0% (seasonal_naive_7d) |
| 2 h | 1.331 | 2.979 | 2.757 | 2.291 | +51.7% (seasonal_naive_7d) |
| 3 h | 1.333 | 2.979 | 2.757 | 2.272 | +51.7% (seasonal_naive_7d) |
| 4 h | 1.337 | 2.979 | 2.757 | 2.267 | +51.5% (seasonal_naive_7d) |
| 5 h | 1.361 | 2.979 | 2.757 | 2.287 | +50.6% (seasonal_naive_7d) |
| 6 h | 1.381 | 2.979 | 2.757 | 2.302 | +49.9% (seasonal_naive_7d) |
| 7 h | 1.402 | 2.979 | 2.757 | 2.333 | +49.1% (seasonal_naive_7d) |
| 8 h | 1.433 | 2.979 | 2.757 | 2.364 | +48.0% (seasonal_naive_7d) |
| 9 h | 1.465 | 2.979 | 2.757 | 2.422 | +46.8% (seasonal_naive_7d) |
| 10 h | 1.471 | 2.979 | 2.757 | 2.437 | +46.6% (seasonal_naive_7d) |
| 11 h | 1.504 | 2.979 | 2.757 | 2.488 | +45.4% (seasonal_naive_7d) |
| 12 h | 1.506 | 2.979 | 2.757 | 2.423 | +45.4% (seasonal_naive_7d) |
| 13 h | 1.511 | 2.979 | 2.757 | 2.389 | +45.2% (seasonal_naive_7d) |
| 14 h | 1.530 | 2.979 | 2.757 | 2.403 | +44.5% (seasonal_naive_7d) |
| 15 h | 1.540 | 2.979 | 2.757 | 2.410 | +44.2% (seasonal_naive_7d) |
| 16 h | 1.547 | 2.979 | 2.757 | 2.417 | +43.9% (seasonal_naive_7d) |
| 17 h | 1.552 | 2.979 | 2.757 | 2.430 | +43.7% (seasonal_naive_7d) |
| 18 h | 1.564 | 2.979 | 2.757 | 2.429 | +43.3% (seasonal_naive_7d) |
| 19 h | 1.567 | 2.979 | 2.757 | 2.429 | +43.2% (seasonal_naive_7d) |
| 20 h | 1.580 | 2.979 | 2.757 | 2.449 | +42.7% (seasonal_naive_7d) |
| 21 h | 1.583 | 2.979 | 2.757 | 2.448 | +42.6% (seasonal_naive_7d) |
| 22 h | 1.589 | 2.979 | 2.757 | 2.462 | +42.4% (seasonal_naive_7d) |
| 23 h | 1.597 | 2.979 | 2.757 | 2.477 | +42.1% (seasonal_naive_7d) |
| 24 h | 1.608 | 2.979 | 2.757 | 2.483 | +41.7% (seasonal_naive_7d) |

## Most important features (gain share)

- **all horizons:** naive24_target 38.0%, target_weekend 12.9%, target_tod_cos 7.4%, naive168_target 6.6%, district_type 6.1%, temp_yday_target 5.6%, target_tod_sin 5.1%, jobs_k 4.4%
