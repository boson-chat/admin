package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Registry tells whether an image tag has been pushed: a GitHub release can
// exist minutes before its image does, and an update is only offered once
// the stack could pull it.
type Registry interface {
	TagExists(ctx context.Context, image, tag string) (bool, error)
}

// OCIRegistry speaks the OCI distribution API (HEAD /v2/<repo>/manifests/<tag>)
// with the anonymous bearer-token flow Harbor and Docker Hub use for
// public repositories.
type OCIRegistry struct {
	HTTP *http.Client
	// Scheme is "https".
	Scheme string
	// Override sends every request to this base URL instead of the image's
	// registry host (tests, or a pull-through mirror): "http://127.0.0.1:5000".
	Override string

	mu    sync.Mutex
	known map[string]regEntry
}

type regEntry struct {
	ok bool
	at time.Time
}

func NewOCIRegistry(timeout time.Duration) *OCIRegistry {
	return &OCIRegistry{HTTP: &http.Client{Timeout: timeout}, Scheme: "https", known: map[string]regEntry{}}
}

const manifestAccept = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, " +
	"application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.manifest.v1+json"

func (r *OCIRegistry) TagExists(ctx context.Context, image, tag string) (bool, error) {
	key := image + ":" + tag
	r.mu.Lock()
	e, hit := r.known[key]
	r.mu.Unlock()
	// A pushed tag stays; a missing one is asked again after 10 minutes.
	if hit && (e.ok || time.Since(e.at) < 10*time.Minute) {
		return e.ok, nil
	}
	host, repo, ok := strings.Cut(image, "/")
	if !ok || !strings.ContainsAny(host, ".:") {
		host, repo = "registry-1.docker.io", image
		if !strings.Contains(repo, "/") {
			repo = "library/" + repo
		}
	}
	base := r.Scheme + "://" + host
	if r.Override != "" {
		base = strings.TrimRight(r.Override, "/")
	}
	u := base + "/v2/" + repo + "/manifests/" + url.PathEscape(tag)
	status, challenge, err := r.head(ctx, u, "")
	if err == nil && status == http.StatusUnauthorized && challenge != "" {
		var tok string
		if tok, err = r.token(ctx, challenge, repo); err == nil {
			status, _, err = r.head(ctx, u, tok)
		}
	}
	if err != nil {
		return false, err
	}
	var exists bool
	switch status {
	case http.StatusOK:
		exists = true
	case http.StatusNotFound:
		exists = false
	default:
		return false, fmt.Errorf("registry %s answered %d for %s", host, status, key)
	}
	r.mu.Lock()
	r.known[key] = regEntry{ok: exists, at: time.Now()}
	r.mu.Unlock()
	return exists, nil
}

func (r *OCIRegistry) head(ctx context.Context, u, token string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Accept", manifestAccept)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("registry unreachable: %w", err)
	}
	resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Www-Authenticate"), nil
}

// token follows a `Bearer realm="…",service="…"` challenge anonymously.
func (r *OCIRegistry) token(ctx context.Context, challenge, repo string) (string, error) {
	scheme, params, _ := strings.Cut(challenge, " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return "", fmt.Errorf("registry wants %s auth", scheme)
	}
	p := map[string]string{}
	for _, kv := range strings.Split(params, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if ok {
			p[strings.ToLower(k)] = strings.Trim(v, `"`)
		}
	}
	if p["realm"] == "" {
		return "", fmt.Errorf("registry challenge has no realm")
	}
	q := url.Values{"scope": {"repository:" + repo + ":pull"}}
	if p["service"] != "" {
		q.Set("service", p["service"])
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p["realm"]+"?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("registry token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry token: %d", resp.StatusCode)
	}
	var t struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&t); err != nil {
		return "", fmt.Errorf("registry token: %w", err)
	}
	if t.Token == "" {
		t.Token = t.AccessToken
	}
	return t.Token, nil
}
