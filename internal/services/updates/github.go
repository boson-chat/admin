package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ReleaseSource lists a repository's releases.
type ReleaseSource interface {
	Releases(ctx context.Context, repo string) ([]Release, error)
	// Limit is the last rate limit GitHub reported (nil before any call).
	Limit() *RateLimit
}

// RateLimit is GitHub's X-RateLimit-* for the panel's requests.
type RateLimit struct {
	Limit     int       `json:"limit"`
	Remaining int       `json:"remaining"`
	Reset     time.Time `json:"reset"`
	// Authenticated: a token is configured (5,000 an hour instead of 60).
	Authenticated bool `json:"authenticated"`
}

// ErrRateLimited: GitHub refused for now; the cached releases are used.
type ErrRateLimited struct{ Reset time.Time }

func (e ErrRateLimited) Error() string {
	return "GitHub's API rate limit is used up until " + e.Reset.UTC().Format("15:04 UTC") + "; set UPDATES_GITHUB_TOKEN for a higher limit"
}

// ErrNoReleases: the repository doesn't exist (or isn't public) or has no feed.
type ErrNoReleases struct{ Repo string }

func (e ErrNoReleases) Error() string { return "no releases published at github.com/" + e.Repo }

// GitHub reads releases from the GitHub REST API with conditional requests
// (ETag: a 304 costs nothing against the limit when authenticated) and keeps
// the last good answer per repository for when GitHub is down or limits us.
type GitHub struct {
	BaseURL string // https://api.github.com
	Token   string // optional
	HTTP    *http.Client
	// MinAge: a repository fetched less than this ago is served from cache.
	MinAge time.Duration

	mu    sync.Mutex
	cache map[string]*ghEntry
	limit *RateLimit
}

type ghEntry struct {
	etag     string
	releases []Release
	at       time.Time
	missing  bool
}

// NewGitHub returns a client for baseURL ("" = api.github.com).
func NewGitHub(baseURL, token string, timeout time.Duration) *GitHub {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &GitHub{BaseURL: strings.TrimRight(baseURL, "/"), Token: strings.TrimSpace(token),
		HTTP: &http.Client{Timeout: timeout}, MinAge: 5 * time.Minute, cache: map[string]*ghEntry{}}
}

func (g *GitHub) Limit() *RateLimit {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.limit == nil {
		return nil
	}
	l := *g.limit
	return &l
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
}

// Releases returns the newest 100 releases (drafts left out), newest first.
func (g *GitHub) Releases(ctx context.Context, repo string) ([]Release, error) {
	g.mu.Lock()
	e := g.cache[repo]
	lim := g.limit
	g.mu.Unlock()
	if e != nil && time.Since(e.at) < g.MinAge {
		return e.cached(repo)
	}
	if lim != nil && lim.Remaining == 0 && time.Now().Before(lim.Reset) {
		if e != nil && !e.missing && len(e.releases) > 0 {
			return e.releases, ErrRateLimited{Reset: lim.Reset}
		}
		return nil, ErrRateLimited{Reset: lim.Reset}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.BaseURL+"/repos/"+repo+"/releases?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "boson-admin-updates")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	if e != nil && e.etag != "" {
		req.Header.Set("If-None-Match", e.etag)
	}
	resp, err := g.HTTP.Do(req)
	if err != nil {
		if e != nil {
			r, _ := e.cached(repo)
			return r, fmt.Errorf("GitHub unreachable (showing the last answer): %w", err)
		}
		return nil, fmt.Errorf("GitHub unreachable: %w", err)
	}
	defer resp.Body.Close()
	g.noteLimit(resp.Header)
	switch {
	case resp.StatusCode == http.StatusNotModified && e != nil:
		g.store(repo, &ghEntry{etag: e.etag, releases: e.releases, missing: e.missing, at: time.Now()})
		return e.cached(repo)
	case resp.StatusCode == http.StatusNotFound:
		g.store(repo, &ghEntry{missing: true, at: time.Now()})
		return nil, ErrNoReleases{Repo: repo}
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		reset := time.Now().Add(time.Hour)
		if l := g.Limit(); l != nil && !l.Reset.IsZero() {
			reset = l.Reset
		}
		if ra, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
			reset = time.Now().Add(time.Duration(ra) * time.Second)
		}
		g.mu.Lock()
		if g.limit == nil {
			g.limit = &RateLimit{Authenticated: g.Token != ""}
		}
		g.limit.Remaining, g.limit.Reset = 0, reset
		g.mu.Unlock()
		if e != nil {
			return e.releases, ErrRateLimited{Reset: reset}
		}
		return nil, ErrRateLimited{Reset: reset}
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("GitHub answered %d for %s", resp.StatusCode, repo)
	}
	var raw []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("GitHub: decoding %s releases: %w", repo, err)
	}
	rs := make([]Release, 0, len(raw))
	for _, r := range raw {
		if r.Draft {
			continue
		}
		rs = append(rs, Release{Tag: r.TagName, Name: r.Name, Body: r.Body, URL: r.HTMLURL, PublishedAt: r.PublishedAt})
	}
	rs = SortReleases(rs)
	g.store(repo, &ghEntry{etag: resp.Header.Get("ETag"), releases: rs, at: time.Now()})
	return rs, nil
}

func (e *ghEntry) cached(repo string) ([]Release, error) {
	if e.missing {
		return nil, ErrNoReleases{Repo: repo}
	}
	return e.releases, nil
}

func (g *GitHub) store(repo string, e *ghEntry) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cache[repo] = e
}

func (g *GitHub) noteLimit(h http.Header) {
	lim, err1 := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	rem, err2 := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	if err1 != nil || err2 != nil {
		return
	}
	l := &RateLimit{Limit: lim, Remaining: rem, Authenticated: g.Token != ""}
	if r, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		l.Reset = time.Unix(r, 0)
	}
	g.mu.Lock()
	g.limit = l
	g.mu.Unlock()
}
