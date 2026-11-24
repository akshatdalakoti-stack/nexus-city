package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------- response

// InfraInfo is the live state of the deployment, read from the Kubernetes
// API (and Prometheus for per-pod usage). Nothing in it is estimated.
type InfraInfo struct {
	Mode        string       `json:"mode"` // kubernetes | outside-kubernetes
	Namespace   string       `json:"namespace,omitempty"`
	CheckedAt   time.Time    `json:"checked_at"`
	Error       string       `json:"error,omitempty"`
	Workloads   []Workload   `json:"workloads"`
	Pods        []PodInfo    `json:"pods"`
	Autoscalers []Autoscaler `json:"autoscalers"`
	// Where per-pod CPU and memory came from: metrics-server, prometheus or none.
	UsageSource string           `json:"usage_source"`
	Prometheus  PrometheusStatus `json:"prometheus"`
	GrafanaURL  string           `json:"grafana_url,omitempty"`
	// APIs the gateway could not read (RBAC or not installed), so the UI can say why data is missing.
	Missing []string `json:"missing,omitempty"`
}

type Workload struct {
	Kind      string `json:"kind"` // Deployment | StatefulSet
	Name      string `json:"name"`
	Component string `json:"component"`
	Desired   int    `json:"desired"`
	Ready     int    `json:"ready"`
	Available int    `json:"available"`
	Updated   int    `json:"updated"`
	Image     string `json:"image,omitempty"`
}

type PodInfo struct {
	Name      string     `json:"name"`
	Component string     `json:"component"`
	Phase     string     `json:"phase"`
	Status    string     `json:"status"` // Running, Pending, CrashLoopBackOff, ...
	Ready     bool       `json:"ready"`
	Restarts  int        `json:"restarts"`
	Node      string     `json:"node,omitempty"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	CPUm      *float64   `json:"cpu_millicores,omitempty"`
	MemMiB    *float64   `json:"memory_mib,omitempty"`
	CPUReqm   float64    `json:"cpu_request_millicores"`
	CPULimm   float64    `json:"cpu_limit_millicores"`
	MemReqMiB float64    `json:"memory_request_mib"`
	MemLimMiB float64    `json:"memory_limit_mib"`
}

type Autoscaler struct {
	Name            string            `json:"name"`
	Target          string            `json:"target"`
	Min             int               `json:"min"`
	Max             int               `json:"max"`
	Current         *int              `json:"current,omitempty"`
	Desired         *int              `json:"desired,omitempty"`
	Active          *bool             `json:"active,omitempty"`
	Ready           *bool             `json:"ready,omitempty"`
	Trigger         string            `json:"trigger"`
	TriggerMetadata map[string]string `json:"trigger_metadata,omitempty"`
	LastScaleTime   *time.Time        `json:"last_scale_time,omitempty"`
}

type PrometheusStatus struct {
	Configured bool   `json:"configured"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
}

// ---------------------------------------------------------------- API shapes

type k8sMeta struct {
	Name              string            `json:"name"`
	Labels            map[string]string `json:"labels"`
	CreationTimestamp *time.Time        `json:"creationTimestamp"`
}

type k8sResources struct {
	Requests map[string]string `json:"requests"`
	Limits   map[string]string `json:"limits"`
}

type k8sPodList struct {
	Items []struct {
		Metadata k8sMeta `json:"metadata"`
		Spec     struct {
			NodeName   string `json:"nodeName"`
			Containers []struct {
				Name      string       `json:"name"`
				Image     string       `json:"image"`
				Resources k8sResources `json:"resources"`
			} `json:"containers"`
		} `json:"spec"`
		Status struct {
			Phase             string     `json:"phase"`
			Reason            string     `json:"reason"`
			StartTime         *time.Time `json:"startTime"`
			ContainerStatuses []struct {
				Ready        bool `json:"ready"`
				RestartCount int  `json:"restartCount"`
				State        struct {
					Waiting    *struct{ Reason string } `json:"waiting"`
					Terminated *struct{ Reason string } `json:"terminated"`
				} `json:"state"`
			} `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

type k8sWorkloadList struct {
	Items []struct {
		Metadata k8sMeta `json:"metadata"`
		Spec     struct {
			Replicas *int `json:"replicas"`
			Template struct {
				Spec struct {
					Containers []struct {
						Image string `json:"image"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
		Status struct {
			ReadyReplicas     int `json:"readyReplicas"`
			AvailableReplicas int `json:"availableReplicas"`
			UpdatedReplicas   int `json:"updatedReplicas"`
		} `json:"status"`
	} `json:"items"`
}

type k8sScaledObjectList struct {
	Items []struct {
		Metadata k8sMeta `json:"metadata"`
		Spec     struct {
			ScaleTargetRef  struct{ Name string } `json:"scaleTargetRef"`
			MinReplicaCount *int                  `json:"minReplicaCount"`
			MaxReplicaCount *int                  `json:"maxReplicaCount"`
			Triggers        []struct {
				Type     string            `json:"type"`
				Metadata map[string]string `json:"metadata"`
			} `json:"triggers"`
		} `json:"spec"`
		Status struct {
			HPAName    string `json:"hpaName"`
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}

type k8sHPAList struct {
	Items []struct {
		Metadata k8sMeta `json:"metadata"`
		Status   struct {
			CurrentReplicas int        `json:"currentReplicas"`
			DesiredReplicas int        `json:"desiredReplicas"`
			LastScaleTime   *time.Time `json:"lastScaleTime"`
		} `json:"status"`
	} `json:"items"`
}

type k8sPodMetricsList struct {
	Items []struct {
		Metadata   k8sMeta `json:"metadata"`
		Containers []struct {
			Usage map[string]string `json:"usage"`
		} `json:"containers"`
	} `json:"items"`
}

// ---------------------------------------------------------------- quantities

// parseCPU converts a Kubernetes CPU quantity ("250m", "1", "1500000n") to millicores.
func parseCPU(q string) float64 {
	q = strings.TrimSpace(q)
	if q == "" {
		return 0
	}
	mult := 1000.0
	switch {
	case strings.HasSuffix(q, "n"):
		q, mult = q[:len(q)-1], 1e-6
	case strings.HasSuffix(q, "u"):
		q, mult = q[:len(q)-1], 1e-3
	case strings.HasSuffix(q, "m"):
		q, mult = q[:len(q)-1], 1
	}
	v, err := strconv.ParseFloat(q, 64)
	if err != nil {
		return 0
	}
	return v * mult
}

// parseMem converts a Kubernetes memory quantity ("128Mi", "1G", "123456Ki") to MiB.
func parseMem(q string) float64 {
	q = strings.TrimSpace(q)
	if q == "" {
		return 0
	}
	units := []struct {
		suffix string
		bytes  float64
	}{
		{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40},
		{"k", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12},
	}
	mult := 1.0
	for _, u := range units {
		if strings.HasSuffix(q, u.suffix) {
			q, mult = strings.TrimSuffix(q, u.suffix), u.bytes
			break
		}
	}
	v, err := strconv.ParseFloat(q, 64)
	if err != nil {
		return 0
	}
	return v * mult / (1 << 20)
}

func component(m k8sMeta) string {
	if c := m.Labels["app.kubernetes.io/name"]; c != "" {
		return c
	}
	return m.Name
}

// ---------------------------------------------------------------- collector

type infra struct {
	kube    *kubeClient
	kubeErr error
	prom    *promClient
	grafana string

	mu     sync.Mutex
	cached *InfraInfo
	at     time.Time
}

func newInfra(prometheusURL, grafanaURL string) *infra {
	k, err := newInClusterKube()
	in := &infra{kube: k, kubeErr: err, grafana: grafanaURL}
	if prometheusURL != "" {
		in.prom = &promClient{base: strings.TrimRight(prometheusURL, "/"), http: &http.Client{Timeout: 4 * time.Second}}
	}
	return in
}

// snapshot returns the current state, cached for two seconds so many open
// dashboards don't multiply load on the API server.
func (in *infra) snapshot(ctx context.Context) InfraInfo {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.cached != nil && time.Since(in.at) < 2*time.Second {
		return *in.cached
	}
	info := in.collect(ctx)
	in.cached, in.at = &info, time.Now()
	return info
}

func (in *infra) collect(ctx context.Context) InfraInfo {
	info := InfraInfo{CheckedAt: time.Now().UTC(), Workloads: []Workload{}, Pods: []PodInfo{}, Autoscalers: []Autoscaler{},
		UsageSource: "none", GrafanaURL: in.grafana}
	info.Prometheus.Configured = in.prom != nil
	if in.prom != nil {
		if err := in.prom.ping(ctx); err != nil {
			info.Prometheus.Error = err.Error()
		} else {
			info.Prometheus.OK = true
		}
	}
	if in.kube == nil {
		info.Mode = "outside-kubernetes"
		if !errors.Is(in.kubeErr, errNotInCluster) && in.kubeErr != nil {
			info.Error = in.kubeErr.Error()
		}
		return info
	}
	info.Mode, info.Namespace = "kubernetes", in.kube.namespace
	k := in.kube
	missing := func(what string, err error) {
		var se kubeStatusError
		reason := err.Error()
		if errors.As(err, &se) {
			switch se.code {
			case http.StatusForbidden:
				reason = "not permitted (RBAC)"
			case http.StatusNotFound:
				reason = "API not installed"
			}
		}
		info.Missing = append(info.Missing, what+": "+reason)
	}

	var pods k8sPodList
	if err := k.get(ctx, k.nsPath("", "v1", "pods"), &pods); err != nil {
		info.Error = "cannot list pods: " + err.Error()
		return info
	}
	for _, kind := range []struct{ kind, resource string }{{"Deployment", "deployments"}, {"StatefulSet", "statefulsets"}} {
		var wl k8sWorkloadList
		if err := k.get(ctx, k.nsPath("apps", "v1", kind.resource), &wl); err != nil {
			missing(kind.resource, err)
			continue
		}
		for _, w := range wl.Items {
			desired := 1
			if w.Spec.Replicas != nil {
				desired = *w.Spec.Replicas
			}
			img := ""
			if cs := w.Spec.Template.Spec.Containers; len(cs) > 0 {
				img = cs[0].Image
			}
			info.Workloads = append(info.Workloads, Workload{Kind: kind.kind, Name: w.Metadata.Name, Component: component(w.Metadata),
				Desired: desired, Ready: w.Status.ReadyReplicas, Available: w.Status.AvailableReplicas, Updated: w.Status.UpdatedReplicas, Image: img})
		}
	}
	sort.Slice(info.Workloads, func(i, j int) bool { return info.Workloads[i].Name < info.Workloads[j].Name })

	for _, p := range pods.Items {
		pi := PodInfo{Name: p.Metadata.Name, Component: component(p.Metadata), Phase: p.Status.Phase, Status: p.Status.Phase,
			Node: p.Spec.NodeName, StartedAt: p.Status.StartTime, Ready: len(p.Status.ContainerStatuses) > 0}
		if p.Status.Reason != "" {
			pi.Status = p.Status.Reason
		}
		for _, cs := range p.Status.ContainerStatuses {
			pi.Ready = pi.Ready && cs.Ready
			pi.Restarts += cs.RestartCount
			if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
				pi.Status = cs.State.Waiting.Reason
			} else if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
				pi.Status = cs.State.Terminated.Reason
			}
		}
		for _, c := range p.Spec.Containers {
			pi.CPUReqm += parseCPU(c.Resources.Requests["cpu"])
			pi.CPULimm += parseCPU(c.Resources.Limits["cpu"])
			pi.MemReqMiB += parseMem(c.Resources.Requests["memory"])
			pi.MemLimMiB += parseMem(c.Resources.Limits["memory"])
		}
		info.Pods = append(info.Pods, pi)
	}
	sort.Slice(info.Pods, func(i, j int) bool {
		if info.Pods[i].Component != info.Pods[j].Component {
			return info.Pods[i].Component < info.Pods[j].Component
		}
		return info.Pods[i].Name < info.Pods[j].Name
	})

	in.usage(ctx, &info, missing)
	in.autoscalers(ctx, &info, missing)
	return info
}

// usage fills per-pod CPU and memory: metrics-server if installed, else the
// process metrics each service exposes to Prometheus.
func (in *infra) usage(ctx context.Context, info *InfraInfo, missing func(string, error)) {
	idx := map[string]int{}
	for i, p := range info.Pods {
		idx[p.Name] = i
	}
	var pm k8sPodMetricsList
	err := in.kube.get(ctx, in.kube.nsPath("metrics.k8s.io", "v1beta1", "pods"), &pm)
	if err == nil {
		for _, m := range pm.Items {
			i, ok := idx[m.Metadata.Name]
			if !ok {
				continue
			}
			var cpu, mem float64
			for _, c := range m.Containers {
				cpu += parseCPU(c.Usage["cpu"])
				mem += parseMem(c.Usage["memory"])
			}
			info.Pods[i].CPUm, info.Pods[i].MemMiB = &cpu, &mem
		}
		info.UsageSource = "metrics-server"
		return
	}
	if in.prom == nil || !info.Prometheus.OK {
		missing("pod metrics", err)
		return
	}
	ns := in.kube.namespace
	cpu, err1 := in.prom.instantByPod(ctx, fmt.Sprintf(`sum by (pod) (rate(process_cpu_seconds_total{namespace=%q}[2m])) * 1000`, ns))
	mem, err2 := in.prom.instantByPod(ctx, fmt.Sprintf(`sum by (pod) (process_resident_memory_bytes{namespace=%q}) / 1048576`, ns))
	if err1 != nil || err2 != nil {
		missing("pod metrics", errors.Join(err1, err2))
		return
	}
	for name, i := range idx {
		if v, ok := cpu[name]; ok {
			info.Pods[i].CPUm = &v
		}
		if v, ok := mem[name]; ok {
			info.Pods[i].MemMiB = &v
		}
	}
	info.UsageSource = "prometheus"
}

func (in *infra) autoscalers(ctx context.Context, info *InfraInfo, missing func(string, error)) {
	var sol k8sScaledObjectList
	if err := in.kube.get(ctx, in.kube.nsPath("keda.sh", "v1alpha1", "scaledobjects"), &sol); err != nil {
		missing("KEDA scaledobjects", err)
		return
	}
	hpas := map[string]int{}
	var hl k8sHPAList
	if err := in.kube.get(ctx, in.kube.nsPath("autoscaling", "v2", "horizontalpodautoscalers"), &hl); err != nil {
		missing("horizontalpodautoscalers", err)
	}
	for i, h := range hl.Items {
		hpas[h.Metadata.Name] = i
	}
	for _, so := range sol.Items {
		a := Autoscaler{Name: so.Metadata.Name, Target: so.Spec.ScaleTargetRef.Name, Max: 100}
		if so.Spec.MinReplicaCount != nil {
			a.Min = *so.Spec.MinReplicaCount
		}
		if so.Spec.MaxReplicaCount != nil {
			a.Max = *so.Spec.MaxReplicaCount
		}
		if len(so.Spec.Triggers) > 0 {
			a.Trigger = so.Spec.Triggers[0].Type
			a.TriggerMetadata = map[string]string{}
			for k, v := range so.Spec.Triggers[0].Metadata {
				if !strings.Contains(strings.ToLower(k), "password") {
					a.TriggerMetadata[k] = v
				}
			}
		}
		for _, c := range so.Status.Conditions {
			v := c.Status == "True"
			switch c.Type {
			case "Active":
				a.Active = &v
			case "Ready":
				a.Ready = &v
			}
		}
		hpaName := so.Status.HPAName
		if hpaName == "" {
			hpaName = "keda-hpa-" + so.Metadata.Name
		}
		if i, ok := hpas[hpaName]; ok {
			h := hl.Items[i].Status
			cur, des := h.CurrentReplicas, h.DesiredReplicas
			a.Current, a.Desired, a.LastScaleTime = &cur, &des, h.LastScaleTime
		}
		// Scaled to zero: the HPA is inactive, so read replicas from the workload.
		if a.Current == nil || (a.Active != nil && !*a.Active) {
			for _, w := range info.Workloads {
				if w.Name == a.Target {
					cur := w.Desired
					a.Current = &cur
				}
			}
		}
		info.Autoscalers = append(info.Autoscalers, a)
	}
}

// ---------------------------------------------------------------- Prometheus

type promClient struct {
	base string
	http *http.Client
}

type promResult struct {
	Metric map[string]string `json:"metric"`
	Value  [2]any            `json:"value"`
	Values [][2]any          `json:"values"`
}

func (p *promClient) query(ctx context.Context, path string, params url.Values) ([]promResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+path+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Result []promResult `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("prometheus: %w", err)
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("prometheus: %s", body.Error)
	}
	return body.Data.Result, nil
}

func (p *promClient) ping(ctx context.Context) error {
	// vector(1), not 1: a bare number yields a scalar result, not a list.
	_, err := p.query(ctx, "/api/v1/query", url.Values{"query": {"vector(1)"}})
	return err
}

func promFloat(v any) (float64, bool) {
	s, ok := v.(string)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func (p *promClient) instantByPod(ctx context.Context, q string) (map[string]float64, error) {
	res, err := p.query(ctx, "/api/v1/query", url.Values{"query": {q}})
	if err != nil {
		return nil, err
	}
	out := map[string]float64{}
	for _, r := range res {
		if v, ok := promFloat(r.Value[1]); ok && r.Metric["pod"] != "" {
			out[r.Metric["pod"]] = v
		}
	}
	return out, nil
}

// infraSeries are the fixed PromQL queries behind the Infrastructure page's
// charts. Callers pick from this list; arbitrary PromQL is not accepted.
var infraSeries = []struct{ Key, Query string }{
	{"queue_waiting", `max(nexus_scenario_queue_waiting)`},
	{"queue_running", `max(nexus_scenario_queue_running)`},
	{"scenario_workers", `count(up{component="scenario-worker"} == 1) or vector(0)`},
	{"scenarios_per_min", `sum(rate(nexus_scenario_jobs_total{status="done"}[2m])) * 60 or vector(0)`},
	{"gateway_rps", `sum(rate(nexus_gateway_http_requests_total[1m]))`},
	{"sim_steps_per_s", `sum(rate(nexus_sim_steps_total[1m]))`},
	{"cpu_by_component", `sum by (component) (rate(process_cpu_seconds_total[1m])) * 1000`},
	{"memory_by_component", `sum by (component) (process_resident_memory_bytes) / 1048576`},
}

// SeriesPoint is [unix seconds, value].
type SeriesPoint [2]float64

type Series struct {
	Labels map[string]string `json:"labels"`
	Points []SeriesPoint     `json:"points"`
}

func (p *promClient) rangeQuery(ctx context.Context, q string, start, end time.Time, step time.Duration) ([]Series, error) {
	res, err := p.query(ctx, "/api/v1/query_range", url.Values{
		"query": {q}, "start": {strconv.FormatInt(start.Unix(), 10)}, "end": {strconv.FormatInt(end.Unix(), 10)},
		"step": {strconv.Itoa(int(step.Seconds()))},
	})
	if err != nil {
		return nil, err
	}
	out := make([]Series, 0, len(res))
	for _, r := range res {
		s := Series{Labels: r.Metric, Points: make([]SeriesPoint, 0, len(r.Values))}
		delete(s.Labels, "__name__")
		for _, v := range r.Values {
			t, ok1 := v[0].(float64)
			f, ok2 := promFloat(v[1])
			if ok1 && ok2 {
				s.Points = append(s.Points, SeriesPoint{t, f})
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// ---------------------------------------------------------------- handlers

func (s *Server) handleInfra(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.infra.snapshot(ctx))
}

func (s *Server) handleInfraSeries(w http.ResponseWriter, r *http.Request) {
	if s.infra.prom == nil {
		writeError(w, http.StatusServiceUnavailable, "prometheus not configured (set PROMETHEUS_URL)")
		return
	}
	minutes := 30
	if v := r.URL.Query().Get("minutes"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 5 || n > 360 {
			writeError(w, http.StatusBadRequest, "minutes must be 5..360")
			return
		}
		minutes = n
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	end := time.Now()
	start := end.Add(-time.Duration(minutes) * time.Minute)
	step := time.Duration(max(15, minutes*60/120)) * time.Second
	out := map[string]any{"step_s": step.Seconds(), "start": start.Unix(), "end": end.Unix()}
	series := map[string][]Series{}
	var errs []string
	for _, q := range infraSeries {
		res, err := s.infra.prom.rangeQuery(ctx, q.Query, start, end, step)
		if err != nil {
			errs = append(errs, q.Key+": "+err.Error())
			continue
		}
		series[q.Key] = res
	}
	out["series"] = series
	if len(errs) > 0 {
		out["errors"] = errs
	}
	writeJSON(w, http.StatusOK, out)
}
