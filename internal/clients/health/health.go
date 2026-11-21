// Package health probes service health and version endpoints over HTTP.
package health

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Result is one health check.
type Result struct {
	URL        string        `json:"url"`
	OK         bool          `json:"ok"`
	StatusCode int           `json:"status_code,omitempty"`
	Latency    time.Duration `json:"-"`
	LatencyMS  float64       `json:"latency_ms"`
	// Detail is a short excerpt of the body (e.g. {"status":"no signing key"}) or the error.
	Detail string `json:"detail,omitempty"`
}

type HealthCheckerImpl interface {
	// Check GETs url; 2xx and 3xx are healthy.
	Check(ctx context.Context, url string) Result
	// Version reads a version from url: plain text, JSON {"version"}, a
	// Prometheus exposition (target_info's service_version label), or a
	// JavaScript config object's version: "x" (the web client's
	// /boson-config.js).
	Version(ctx context.Context, url string) (string, error)
}

type HealthChecker struct {
	HTTP *http.Client
}

func NewHealthChecker(timeout time.Duration) HealthCheckerImpl {
	return &HealthChecker{HTTP: &http.Client{
		Timeout: timeout,
		// A redirect is an answer; don't follow it to somewhere else.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (h *HealthChecker) Check(ctx context.Context, url string) Result {
	start := time.Now()
	res := Result{URL: url}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		res.Detail = err.Error()
		return res
	}
	req.Header.Set("User-Agent", "boson-admin-healthcheck")
	resp, err := h.HTTP.Do(req)
	res.Latency = time.Since(start)
	res.LatencyMS = float64(res.Latency.Microseconds()) / 1000
	if err != nil {
		res.Detail = shortError(err)
		return res
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	res.StatusCode = resp.StatusCode
	res.OK = resp.StatusCode >= 200 && resp.StatusCode < 400
	res.Detail = excerpt(body)
	return res
}

func (h *HealthChecker) Version(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s", shortError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/plain") && strings.Contains(ct, "version=") ||
		strings.Contains(ct, "openmetrics") || strings.HasSuffix(req.URL.Path, "/metrics") {
		return versionFromExposition(io.LimitReader(resp.Body, 8<<20))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(req.URL.Path, ".js") || strings.Contains(ct, "javascript") {
		return versionFromScript(body)
	}
	s := strings.TrimSpace(string(body))
	if strings.HasPrefix(s, "{") {
		var v struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(body, &v) == nil && v.Version != "" {
			return clean(v.Version), nil
		}
		return "", fmt.Errorf("no version field")
	}
	if s == "" {
		return "", fmt.Errorf("empty response")
	}
	return clean(s), nil
}

// scriptVersion is a config object's version: "x" (or 'x').
var scriptVersion = regexp.MustCompile(`\bversion\s*:\s*["']([^"'\r\n]+)["']`)

// versionFromScript reads version: "x" from a script like
// window.bosonWebConfig = {…, version: "0.88.0"}. "dev" (a build without a
// version) is no version, so the image tag stands in.
func versionFromScript(body []byte) (string, error) {
	m := scriptVersion.FindSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("no version in the script")
	}
	v := clean(string(m[1]))
	if v == "" || v == "dev" {
		return "", fmt.Errorf("unversioned build")
	}
	return v, nil
}

// versionFromExposition finds target_info{...service_version="x"...}.
func versionFromExposition(r io.Reader) (string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "target_info{") {
			continue
		}
		const key = `service_version="`
		i := strings.Index(line, key)
		if i < 0 {
			break
		}
		rest := line[i+len(key):]
		if j := strings.IndexByte(rest, '"'); j >= 0 {
			return clean(rest[:j]), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no target_info service_version")
}

// clean keeps a version string short and printable.
func clean(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s)
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

func excerpt(b []byte) string {
	s := clean(string(b))
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

// shortError trims Go's long "Get \"url\": dial tcp ..." chains to the cause.
func shortError(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && i < len(s)-2 {
		s = s[i+2:]
	}
	if strings.Contains(err.Error(), "Client.Timeout") || strings.Contains(s, "deadline exceeded") {
		return "timed out"
	}
	return s
}
