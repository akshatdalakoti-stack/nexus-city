package gateway

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseQuantities(t *testing.T) {
	for q, want := range map[string]float64{"250m": 250, "1": 1000, "1500000n": 1.5, "0.5": 500, "": 0, "750u": 0.75} {
		if got := parseCPU(q); math.Abs(got-want) > 1e-9 {
			t.Errorf("parseCPU(%q) = %v, want %v", q, got, want)
		}
	}
	for q, want := range map[string]float64{"128Mi": 128, "1Gi": 1024, "2048Ki": 2, "1048576": 1, "1M": 1e6 / (1 << 20)} {
		if got := parseMem(q); math.Abs(got-want) > 1e-9 {
			t.Errorf("parseMem(%q) = %v, want %v", q, got, want)
		}
	}
}

// fakeCluster serves the API paths the gateway reads, in namespace "nexus".
func fakeCluster(t *testing.T, withMetricsServer bool) *httptest.Server {
	t.Helper()
	objs := map[string]any{
		"/api/v1/namespaces/nexus/pods": map[string]any{"items": []any{
			map[string]any{
				"metadata": map[string]any{"name": "gateway-7f9-abc", "labels": map[string]string{"app.kubernetes.io/name": "gateway"}},
				"spec": map[string]any{"nodeName": "docker-desktop", "containers": []any{map[string]any{"name": "gateway",
					"resources": map[string]any{"requests": map[string]string{"cpu": "100m", "memory": "64Mi"}, "limits": map[string]string{"cpu": "1", "memory": "256Mi"}}}}},
				"status": map[string]any{"phase": "Running", "startTime": "2026-11-20T10:00:00Z",
					"containerStatuses": []any{map[string]any{"ready": true, "restartCount": 0, "state": map[string]any{"running": map[string]any{}}}}},
			},
			map[string]any{
				"metadata": map[string]any{"name": "recorder-5d-xyz", "labels": map[string]string{"app.kubernetes.io/name": "recorder"}},
				"spec":     map[string]any{"containers": []any{map[string]any{"name": "recorder"}}},
				"status": map[string]any{"phase": "Running",
					"containerStatuses": []any{map[string]any{"ready": false, "restartCount": 4, "state": map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff"}}}}},
			},
		}},
		"/apis/apps/v1/namespaces/nexus/deployments": map[string]any{"items": []any{
			map[string]any{"metadata": map[string]any{"name": "gateway", "labels": map[string]string{"app.kubernetes.io/name": "gateway"}},
				"spec":   map[string]any{"replicas": 1, "template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"image": "nexus-city/gateway:dev"}}}}},
				"status": map[string]any{"readyReplicas": 1, "availableReplicas": 1, "updatedReplicas": 1}},
			map[string]any{"metadata": map[string]any{"name": "scenario-worker", "labels": map[string]string{"app.kubernetes.io/name": "scenario-worker"}},
				"spec": map[string]any{"replicas": 0}, "status": map[string]any{}},
		}},
		"/apis/keda.sh/v1alpha1/namespaces/nexus/scaledobjects": map[string]any{"items": []any{
			map[string]any{"metadata": map[string]any{"name": "scenario-worker"},
				"spec": map[string]any{"scaleTargetRef": map[string]any{"name": "scenario-worker"}, "minReplicaCount": 0, "maxReplicaCount": 4,
					"triggers": []any{map[string]any{"type": "redis-streams", "metadata": map[string]string{"stream": "nexus:scenarios", "lagCount": "1", "password": "x"}}}},
				"status": map[string]any{"hpaName": "keda-hpa-scenario-worker", "conditions": []any{
					map[string]any{"type": "Ready", "status": "True"}, map[string]any{"type": "Active", "status": "False"}}}},
		}},
		"/apis/autoscaling/v2/namespaces/nexus/horizontalpodautoscalers": map[string]any{"items": []any{
			map[string]any{"metadata": map[string]any{"name": "keda-hpa-scenario-worker"}, "status": map[string]any{"currentReplicas": 1, "desiredReplicas": 1}},
		}},
	}
	if withMetricsServer {
		objs["/apis/metrics.k8s.io/v1beta1/namespaces/nexus/pods"] = map[string]any{"items": []any{
			map[string]any{"metadata": map[string]any{"name": "gateway-7f9-abc"}, "containers": []any{map[string]any{"usage": map[string]string{"cpu": "12500000n", "memory": "30Mi"}}}},
		}}
	}
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		obj, ok := objs[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(obj)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func testInfra(t *testing.T, cluster *httptest.Server, promURL string) *infra {
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte("test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := newInfra(promURL, "http://localhost:30300")
	in.kube = &kubeClient{base: cluster.URL, namespace: "nexus", tokenPath: tok, http: cluster.Client()}
	return in
}

func TestInfraFromKubernetesAPI(t *testing.T) {
	in := testInfra(t, fakeCluster(t, true), "")
	info := in.collect(context.Background())
	if info.Mode != "kubernetes" || info.Namespace != "nexus" || info.Error != "" {
		t.Fatalf("mode=%s ns=%s err=%s", info.Mode, info.Namespace, info.Error)
	}
	if len(info.Workloads) != 2 || info.Workloads[0].Name != "gateway" || info.Workloads[0].Image != "nexus-city/gateway:dev" {
		t.Fatalf("workloads: %+v", info.Workloads)
	}
	gw, rec := info.Pods[0], info.Pods[1]
	if gw.Component != "gateway" || !gw.Ready || gw.CPUReqm != 100 || gw.CPULimm != 1000 || gw.MemLimMiB != 256 {
		t.Fatalf("gateway pod: %+v", gw)
	}
	if info.UsageSource != "metrics-server" || gw.CPUm == nil || math.Abs(*gw.CPUm-12.5) > 1e-9 || *gw.MemMiB != 30 {
		t.Fatalf("usage: %s %+v", info.UsageSource, gw)
	}
	if rec.Status != "CrashLoopBackOff" || rec.Ready || rec.Restarts != 4 || rec.CPUm != nil {
		t.Fatalf("recorder pod: %+v", rec)
	}
	if len(info.Autoscalers) != 1 {
		t.Fatalf("autoscalers: %+v", info.Autoscalers)
	}
	a := info.Autoscalers[0]
	if a.Trigger != "redis-streams" || a.Min != 0 || a.Max != 4 || a.Active == nil || *a.Active || a.Current == nil {
		t.Fatalf("autoscaler: %+v", a)
	}
	// Inactive (scaled to zero): replicas come from the workload, not the stale HPA status.
	if *a.Current != 0 {
		t.Fatalf("current replicas = %d, want 0", *a.Current)
	}
	if _, leaked := a.TriggerMetadata["password"]; leaked {
		t.Fatal("trigger password must not be exposed")
	}
	if !strings.Contains(strings.Join(info.Missing, ";"), "statefulsets: API not installed") {
		t.Fatalf("missing: %v", info.Missing)
	}
}

func TestInfraUsageFallsBackToPrometheus(t *testing.T) {
	var queries []string
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		queries = append(queries, q)
		if !strings.Contains(q, "(") { // like real Prometheus: a bare number is a scalar, not a vector
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "scalar",
				"result": []any{float64(time.Now().Unix()), q}}})
			return
		}
		val := "1"
		switch {
		case strings.Contains(q, "process_cpu_seconds_total"):
			val = "42.5"
		case strings.Contains(q, "process_resident_memory_bytes"):
			val = "61.25"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector",
			"result": []any{map[string]any{"metric": map[string]string{"pod": "gateway-7f9-abc"}, "value": []any{float64(time.Now().Unix()), val}}}}})
	}))
	defer prom.Close()
	info := testInfra(t, fakeCluster(t, false), prom.URL).collect(context.Background())
	if info.UsageSource != "prometheus" || !info.Prometheus.OK {
		t.Fatalf("usage source %s, prometheus %+v", info.UsageSource, info.Prometheus)
	}
	gw := info.Pods[0]
	if gw.CPUm == nil || *gw.CPUm != 42.5 || *gw.MemMiB != 61.25 {
		t.Fatalf("gateway usage: %+v", gw)
	}
	if !strings.Contains(strings.Join(queries, "\n"), `namespace="nexus"`) {
		t.Fatalf("queries not scoped to the namespace: %v", queries)
	}
}

func TestInfraOutsideKubernetes(t *testing.T) {
	ts := httptest.NewServer(New(Config{}, nil).Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/infra")
	if err != nil {
		t.Fatal(err)
	}
	var info InfraInfo
	_ = json.NewDecoder(resp.Body).Decode(&info)
	resp.Body.Close()
	if info.Mode != "outside-kubernetes" || len(info.Pods) != 0 || info.UsageSource != "none" {
		t.Fatalf("%+v", info)
	}
	resp, _ = http.Get(ts.URL + "/api/infra/series")
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("series without prometheus: %d", resp.StatusCode)
	}
}
