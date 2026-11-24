import json

DS = {"type": "prometheus", "uid": "prometheus"}
panels = []
pid = 0
y = 0


def row(title):
    global pid, y
    pid += 1
    panels.append({"id": pid, "type": "row", "title": title, "collapsed": False,
                   "gridPos": {"h": 1, "w": 24, "x": 0, "y": y}, "panels": []})
    y += 1


def ts(title, targets, x, w=8, h=8, unit="short", desc="", stack=False, decimals=None):
    global pid
    pid += 1
    p = {
        "id": pid, "type": "timeseries", "title": title, "description": desc, "datasource": DS,
        "gridPos": {"h": h, "w": w, "x": x, "y": y},
        "fieldConfig": {"defaults": {"unit": unit, "min": 0,
                                     "custom": {"lineWidth": 2, "fillOpacity": 12, "showPoints": "never",
                                                "stacking": {"mode": "normal" if stack else "none"}}},
                        "overrides": []},
        "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi"}},
        "targets": [{"refId": chr(65 + i), "datasource": DS, "expr": e, "legendFormat": l} for i, (e, l) in enumerate(targets)],
    }
    if decimals is not None:
        p["fieldConfig"]["defaults"]["decimals"] = decimals
    panels.append(p)


def stat(title, expr, x, w=4, h=4, unit="short", desc="", thresholds=None):
    global pid
    pid += 1
    panels.append({
        "id": pid, "type": "stat", "title": title, "description": desc, "datasource": DS,
        "gridPos": {"h": h, "w": w, "x": x, "y": y},
        "fieldConfig": {"defaults": {"unit": unit, "thresholds": {"mode": "absolute", "steps": thresholds or [{"color": "green", "value": None}]}},
                        "overrides": []},
        "options": {"reduceOptions": {"calcs": ["lastNotNull"]}, "colorMode": "value", "graphMode": "area"},
        "targets": [{"refId": "A", "datasource": DS, "expr": expr}],
    })


row("Overview")
stat("Targets up", 'sum(up{job="nexus-pods"})', 0, desc="Pods Prometheus is scraping successfully.")
stat("Targets down", 'count(up{job="nexus-pods"} == 0) or vector(0)', 4,
     thresholds=[{"color": "green", "value": None}, {"color": "red", "value": 1}])
stat("Scenario workers", 'count(up{component="scenario-worker"} == 1) or vector(0)', 8, desc="Running worker pods (KEDA scales 0-3).")
stat("Queue waiting", "max(nexus_scenario_queue_waiting)", 12,
     thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 3}])
stat("Sim speed-up", "max(nexus_sim_speed)", 16, unit="x", desc="Simulated seconds per wall second (the clock setting).")
stat("WebSocket clients", "sum(nexus_gateway_ws_clients)", 20)
y += 4

row("Scenario queue and KEDA autoscaling")
ts("Queue vs workers", [("max(nexus_scenario_queue_waiting)", "waiting"), ("max(nexus_scenario_queue_running)", "running"),
                        ('count(up{component="scenario-worker"} == 1) or vector(0)', "worker pods")], 0, w=12,
   desc="KEDA adds a worker per waiting scenario (max 3) and scales to zero when the queue is empty.")
ts("Scenarios finished", [('sum by (status) (rate(nexus_scenario_jobs_total[2m])) * 60', "{{status}} / min")], 12, w=6)
ts("Scenario run time", [('histogram_quantile(0.5, sum by (le) (rate(nexus_scenario_job_duration_seconds_bucket[10m])))', "p50"),
                         ('histogram_quantile(0.95, sum by (le) (rate(nexus_scenario_job_duration_seconds_bucket[10m])))', "p95")],
   18, w=6, unit="s")
y += 8

row("Simulation and gateway")
ts("Simulation steps per second", [("sum(rate(nexus_sim_steps_total[1m]))", "steps/s")], 0,
   desc="One step is one simulated second. Should track the clock speed setting.")
ts("Share of a core spent stepping", [("sum(rate(nexus_sim_step_seconds_total[1m]))", "cores")], 8, unit="percentunit")
ts("Stream throughput", [("sum by (stream) (rate(nexus_stream_published_total[1m]))", "{{stream}}")], 16, unit="ops")
y += 8
ts("Gateway requests by route", [('topk(8, sum by (route) (rate(nexus_gateway_http_requests_total[2m])))', "{{route}}")], 0, unit="reqps")
ts("Gateway p95 latency", [('histogram_quantile(0.95, sum by (le, route) (rate(nexus_gateway_http_request_duration_seconds_bucket[5m])))', "{{route}}")],
   8, unit="s")
ts("Gateway errors (5xx)", [('sum by (route) (rate(nexus_gateway_http_requests_total{code=~"5.."}[5m]))', "{{route}}")], 16, unit="reqps")
y += 8

row("Models and City Intelligence")
ts("Inference time p95", [('histogram_quantile(0.95, sum by (le, model) (rate(nexus_ml_inference_seconds_bucket[10m])))', "{{model}}")], 0, unit="s")
ts("Forecasts published", [("sum by (model) (rate(nexus_ml_outputs_published_total[5m])) * 60", "{{model}} / min")], 8)
ts("Assistant answers", [('sum by (mode) (rate(nexus_assistant_chats_total[10m])) * 60', "{{mode}} / min"),
                         ('histogram_quantile(0.95, sum by (le) (rate(nexus_assistant_chat_duration_seconds_bucket[30m])))', "p95 seconds")],
   16, desc="Ungrounded numbers flagged: nexus_assistant_ungrounded_numbers_total.")
y += 8

row("Resources (process metrics of each service)")
ts("CPU by component", [("sum by (component) (rate(process_cpu_seconds_total[1m]))", "{{component}}")], 0, w=12, unit="cores", stack=True)
ts("Memory by component", [("sum by (component) (process_resident_memory_bytes)", "{{component}}")], 12, w=12, unit="bytes", stack=True)
y += 8
ts("Recorder", [('sum by (stream) (rate(nexus_recorder_messages_stored_total[5m]))', "{{stream}}"),
                ("rate(nexus_recorder_errors_total[5m])", "errors")], 0, w=12, unit="ops")
ts("Go heap in use", [('sum by (component) (go_memstats_heap_inuse_bytes)', "{{component}}")], 12, w=12, unit="bytes")
y += 8

dash = {
    "uid": "nexus-city", "title": "Nexus City", "tags": ["nexus-city"], "timezone": "browser", "schemaVersion": 41,
    "editable": False, "refresh": "10s", "time": {"from": "now-30m", "to": "now"},
    "description": "Live metrics of the Nexus City digital twin services. All city data is synthetic.",
    "panels": panels,
}
import pathlib
out = pathlib.Path(__file__).with_name("nexus-city.json")
with open(out, "w", encoding="utf-8", newline="\n") as f:
    json.dump(dash, f, indent=1)
print(len(panels), "panels")
