package gateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// kubeClient is a minimal read-only Kubernetes API client using the pod's
// service account. The gateway only lists objects in its own namespace, so
// it doesn't need client-go.
type kubeClient struct {
	base      string
	namespace string
	tokenPath string
	http      *http.Client
}

const saDir = "/var/run/secrets/kubernetes.io/serviceaccount"

var errNotInCluster = errors.New("not running in Kubernetes")

// newInClusterKube returns a client when running inside a pod.
func newInClusterKube() (*kubeClient, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, errNotInCluster
	}
	ns, err := os.ReadFile(saDir + "/namespace")
	if err != nil {
		return nil, fmt.Errorf("service account namespace: %w", err)
	}
	ca, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("service account CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("service account CA is not valid PEM")
	}
	return &kubeClient{
		base:      "https://" + net.JoinHostPort(host, port),
		namespace: strings.TrimSpace(string(ns)),
		tokenPath: saDir + "/token",
		http: &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		}},
	}, nil
}

// kubeStatusError carries the API server's status code (403 = RBAC denied,
// 404 = API not installed, e.g. KEDA or metrics-server missing).
type kubeStatusError struct{ code int }

func (e kubeStatusError) Error() string { return fmt.Sprintf("kubernetes API returned %d", e.code) }

// get fetches path and decodes JSON into out. The token is re-read on each
// call because projected service account tokens rotate.
func (k *kubeClient) get(ctx context.Context, path string, out any) error {
	tok, err := os.ReadFile(k.tokenPath)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	req.Header.Set("Accept", "application/json")
	resp, err := k.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return kubeStatusError{resp.StatusCode}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

func (k *kubeClient) nsPath(group, version, resource string) string {
	if group == "" {
		return fmt.Sprintf("/api/%s/namespaces/%s/%s", version, k.namespace, resource)
	}
	return fmt.Sprintf("/apis/%s/%s/namespaces/%s/%s", group, version, k.namespace, resource)
}
