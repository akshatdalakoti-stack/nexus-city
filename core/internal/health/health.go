// Package health provides the `healthcheck` subcommand used by container
// health checks. The images are distroless (no shell or curl), so each
// binary can probe its own HTTP health endpoint.
package health

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

// RunIfRequested exits the process with the probe result when the binary
// was started as `<binary> healthcheck`. Otherwise it returns immediately.
func RunIfRequested(url string) {
	if len(os.Args) < 2 || os.Args[1] != "healthcheck" {
		return
	}
	client := http.Client{Timeout: 2 * time.Second}
	res, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "unhealthy:", err)
		os.Exit(1)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "unhealthy: status", res.StatusCode)
		os.Exit(1)
	}
	os.Exit(0)
}

// LocalURL turns a listen address such as ":8080" into a loopback URL.
func LocalURL(addr, path string) string {
	if len(addr) > 0 && addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	return "http://" + addr + path
}
