# anomaly model — anomaly-20261119.0839-e8d467

Isolation Forest anomaly detection for roads, districts and air-quality stations.

> Unsupervised detectors; only the alert threshold is tuned, on validation labels. Labels are the simulation's injected events. Offline sensors are trivially detectable (missing values); stuck sensors and emission releases are the informative air-quality cases.

## traffic (road)

| Split | Detector | Precision | Recall | F1 | Flag rate | Events caught |
|---|---|---|---|---|---|---|
| test | Isolation Forest | 0.073 | 0.409 | 0.123 | 0.99% | 118/136 |
| test | Rule baseline | 0.086 | 0.626 | 0.152 | 1.27% | 131/136 |
| holdout | Isolation Forest | 0.083 | 0.456 | 0.140 | 1.30% | 180/217 |
| holdout | Rule baseline | 0.079 | 0.588 | 0.140 | 1.74% | 191/217 |

Features: congestion, cong_vs_yday, cong_delta15, speed_ratio, speed_vs_nbr, speed_vs_yday, flow_vs_yday

## energy (district)

| Split | Detector | Precision | Recall | F1 | Flag rate | Events caught |
|---|---|---|---|---|---|---|
| test | Isolation Forest | 0.311 | 0.273 | 0.291 | 0.78% | 22/22 |
| test | Rule baseline | 0.861 | 0.689 | 0.766 | 0.71% | 19/22 |
| holdout | Isolation Forest | 0.282 | 0.356 | 0.314 | 1.13% | 28/28 |
| holdout | Rule baseline | 0.206 | 0.640 | 0.312 | 2.78% | 22/28 |

Features: vs_yday, vs_last_hour, step5, step15, utilization

## air (station)

| Split | Detector | Precision | Recall | F1 | Flag rate | Events caught |
|---|---|---|---|---|---|---|
| test | Isolation Forest | 0.705 | 0.358 | 0.475 | 2.63% | 128/169 |
| test | Rule baseline | 0.948 | 0.577 | 0.718 | 3.15% | 123/169 |
| holdout | Isolation Forest | 0.657 | 0.335 | 0.444 | 3.09% | 165/240 |
| holdout | Rule baseline | 0.945 | 0.490 | 0.646 | 3.14% | 153/240 |

Features: missing, log_vs_others, delta15_rel, std30, flat30
