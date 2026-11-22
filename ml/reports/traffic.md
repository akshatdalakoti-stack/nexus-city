# traffic model — traffic-20261119.0835-e54650

Road congestion 15, 30 and 60 minutes ahead. Output: congestion index (0–1).

> Trained and evaluated on synthetic Nexus City simulation data. Scores measure how well the model learned the simulator, not real-world forecasting skill.

## Data

- Training seeds [101, 102, 103] — days (0, 20) train, (20, 24) validation (early stopping), (24, 28) test.
- Holdout seed [104]: a different city layout never seen in training.
- Rows whose target falls into a later split are dropped (embargo). The live city (seed 42) is never used.
- Rows: {'15': {'train': 1226241, 'val': 244737, 'test': 244737, 'holdout': 286272}, '30': {'train': 1225602, 'val': 244098, 'test': 244098, 'holdout': 286059}, '60': {'train': 1224324, 'val': 242820, 'test': 242820, 'holdout': 285633}}

## Test results

| Horizon | XGBoost MAE | persistence MAE | same_time_yesterday MAE | ridge MAE | XGBoost RMSE | Improvement vs best baseline |
|---|---|---|---|---|---|---|
| 15 min | 0.043 | 0.053 | 0.066 | 0.049 | 0.089 | +10.9% (ridge) |
| 30 min | 0.045 | 0.057 | 0.066 | 0.051 | 0.092 | +12.0% (ridge) |
| 60 min | 0.046 | 0.062 | 0.066 | 0.054 | 0.094 | +15.8% (ridge) |

Harder subsets (MAE): weekday rush hours, and rows where congestion changes by more than 0.1:

| Horizon | Subset | XGBoost | persistence | same_time_yesterday | ridge |
|---|---|---|---|---|---|
| 15 min | peak | 0.140 | 0.171 | 0.175 | 0.155 |
| 15 min | changing | 0.158 | 0.271 | 0.192 | 0.180 |
| 30 min | peak | 0.150 | 0.192 | 0.177 | 0.166 |
| 30 min | changing | 0.161 | 0.289 | 0.191 | 0.185 |
| 60 min | peak | 0.149 | 0.211 | 0.170 | 0.169 |
| 60 min | changing | 0.152 | 0.300 | 0.179 | 0.185 |

## Holdout results

| Horizon | XGBoost MAE | persistence MAE | same_time_yesterday MAE | ridge MAE | XGBoost RMSE | Improvement vs best baseline |
|---|---|---|---|---|---|---|
| 15 min | 0.050 | 0.061 | 0.070 | 0.056 | 0.102 | +9.6% (ridge) |
| 30 min | 0.053 | 0.066 | 0.070 | 0.059 | 0.107 | +10.8% (ridge) |
| 60 min | 0.054 | 0.073 | 0.071 | 0.063 | 0.109 | +15.1% (ridge) |

Harder subsets (MAE): weekday rush hours, and rows where congestion changes by more than 0.1:

| Horizon | Subset | XGBoost | persistence | same_time_yesterday | ridge |
|---|---|---|---|---|---|
| 15 min | peak | 0.141 | 0.170 | 0.179 | 0.152 |
| 15 min | changing | 0.172 | 0.290 | 0.216 | 0.193 |
| 30 min | peak | 0.153 | 0.191 | 0.182 | 0.164 |
| 30 min | changing | 0.175 | 0.309 | 0.218 | 0.199 |
| 60 min | peak | 0.150 | 0.209 | 0.175 | 0.168 |
| 60 min | changing | 0.164 | 0.319 | 0.204 | 0.200 |

## Most important features (gain share)

- **15 min:** cong_mean30 32.4%, district_cong 9.3%, cong 8.6%, incident 4.5%, weekend 3.2%, tod_cos 3.1%, target_tod_cos 2.7%, road_class 2.4%
- **30 min:** cong_mean30 29.4%, target_tod_sin 7.3%, district_cong 6.6%, tod_sin 6.2%, weekend 5.1%, cong 4.6%, cong_yday_target 4.0%, tod_cos 4.0%
- **60 min:** cong_mean30 21.1%, target_tod_sin 7.9%, tod_cos 7.2%, target_weekend 6.7%, weekend 6.4%, tod_sin 6.2%, cong_yday_target 5.7%, cong 4.9%
