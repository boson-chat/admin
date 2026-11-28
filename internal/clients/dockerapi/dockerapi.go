// Package dockerapi is a small client for the Docker Engine API, spoken
// through a socket proxy that only allows listing, inspecting, starting,
// stopping and restarting containers and reading their logs and stats.
// The panel never mounts the Docker socket itself.
package dockerapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

// Compose labels.
const (
	LabelProject = "com.docker.compose.project"
	LabelService = "com.docker.compose.service"
	LabelOneOff  = "com.docker.compose.oneoff"
	LabelVersion = "org.opencontainers.image.version"
)

// Container is one row of GET /containers/json.
type Container struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	ImageID string            `json:"ImageID"`
	Labels  map[string]string `json:"Labels"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Created int64             `json:"Created"`
}

// Name is the container name without the leading slash.
func (c Container) Name() string {
	if len(c.Names) == 0 {
		return c.ID
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

// Inspect is the part of GET /containers/{id}/json the panel uses.
type Inspect struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
	// Image is the image ID (sha256:…) the container was created from.
	Image        string `json:"Image"`
	RestartCount int    `json:"RestartCount"`
	State        struct {
		Status     string    `json:"Status"`
		Running    bool      `json:"Running"`
		Restarting bool      `json:"Restarting"`
		OOMKilled  bool      `json:"OOMKilled"`
		ExitCode   int       `json:"ExitCode"`
		StartedAt  time.Time `json:"StartedAt"`
		FinishedAt time.Time `json:"FinishedAt"`
		Health     *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
		Tty    bool              `json:"Tty"`
	} `json:"Config"`
}

// Info is the part of GET /info the usage stats read: the host's size.
// Nothing else of it (hostname, kernel, OS, IDs) is decoded.
type Info struct {
	NCPU          int    `json:"NCPU"`
	MemTotal      int64  `json:"MemTotal"`
	Architecture  string `json:"Architecture"`
	DockerRootDir string `json:"DockerRootDir"`
}

// Stats is a computed resource snapshot.
type Stats struct {
	CPUPercent  float64 `json:"cpu_percent"`
	MemoryBytes uint64  `json:"memory_bytes"`
	MemoryLimit uint64  `json:"memory_limit"`
}

// LogOptions selects log lines.
type LogOptions struct {
	Tail  int       // 0 = all
	Since time.Time // exclusive lower bound; zero = from the start
	Tty   bool      // the container has a TTY (logs are not multiplexed)
}

// LogLine is one line of output.
type LogLine struct {
	At     time.Time `json:"at"`
	Stream string    `json:"stream"` // stdout | stderr
	Text   string    `json:"text"`
}

// DockerClientImpl is everything the panel asks of Docker.
type DockerClientImpl interface {
	// List returns the containers of a compose project, running or not.
	List(ctx context.Context, project string) ([]Container, error)
	Inspect(ctx context.Context, id string) (*Inspect, error)
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string) error
	Restart(ctx context.Context, id string) error
	Logs(ctx context.Context, id string, o LogOptions) ([]LogLine, error)
	Stats(ctx context.Context, id string) (*Stats, error)
	// Ping asks GET /_ping: nil when the proxy answers.
	Ping(ctx context.Context) error
}

// Observer is told about every call (for metrics).
type Observer func(op string, status int, err error, d time.Duration)

type Client struct {
	BaseURL string
	HTTP    *http.Client
	Observe Observer
	// MaxLogBytes caps one logs response.
	MaxLogBytes int64
	// RetryWindow: a call that fails because the proxy can't be reached
	// for a moment (its name doesn't resolve, the connection is refused or
	// reset, the answer ends early: IsTransient) is tried again, with
	// backoff, for up to this long. 0 = never. Docker's embedded DNS has
	// no record for a container while compose replaces it, which is
	// exactly when an update needs the proxies. An answer (any status,
	// 403 included) is never retried.
	RetryWindow time.Duration
	// RetryBackoff is the first wait between tries (doubling, at most
	// 5 s). Default 250 ms.
	RetryBackoff time.Duration
}

// DefaultRetryWindow is how long the update runner's clients keep trying
// an unreachable proxy (WithRetry).
const DefaultRetryWindow = 45 * time.Second

// Option configures a client.
type Option func(*Client)

// WithRetry retries transient proxy errors for up to window (RetryWindow).
func WithRetry(window time.Duration) Option { return func(c *Client) { c.RetryWindow = window } }

func newClient(baseURL string, timeout time.Duration, observe Observer, opts []Option) *Client {
	c := &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: timeout}, Observe: observe, MaxLogBytes: 16 << 20}
	for _, o := range opts {
		o(c)
	}
	return c
}

// NewDockerClient talks to the socket proxy at baseURL (http://host:2375).
func NewDockerClient(baseURL string, timeout time.Duration, observe Observer, opts ...Option) DockerClientImpl {
	return newClient(baseURL, timeout, observe, opts)
}

// ErrUnavailable: the proxy can't be reached.
var ErrUnavailable = apperr.New(http.StatusBadGateway, "docker_unavailable", "the Docker socket proxy is unreachable")

func (c *Client) request(ctx context.Context, op, method, path string, limit int64) ([]byte, error) {
	start := time.Now()
	status, body, err := c.roundTrip(ctx, method, path, limit)
	if c.Observe != nil {
		c.Observe(op, status, err, time.Since(start))
	}
	return body, err
}

// IsTransient: err says the proxy couldn't be reached just now (a DNS
// failure, a refused, reset or aborted connection, an answer cut short),
// not that it answered.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return true
	}
	for _, e := range []error{syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.ECONNABORTED, syscall.EPIPE,
		syscall.EHOSTUNREACH, syscall.ENETUNREACH, io.EOF, io.ErrUnexpectedEOF} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// notSent: err happened before the request left (resolving or dialling).
func notSent(err error) bool {
	var dns *net.DNSError
	var op *net.OpError
	return errors.As(err, &dns) || errors.As(err, &op) && op.Op == "dial"
}

// idempotent: trying the call twice does no harm. Creating a container
// doesn't qualify (the second try finds the name taken), so it is only
// retried when the first try never reached the proxy.
func idempotent(method, path string) bool {
	return method != http.MethodPost || !strings.HasPrefix(path, "/containers/create")
}

// exchange sends one call and reads its answer, retrying transient errors
// within RetryWindow. A non-nil error means no answer (wrapping
// ErrUnavailable and the cause); any status is the caller's to read.
func (c *Client) exchange(ctx context.Context, method, path string, body []byte, limit int64) (int, []byte, error) {
	var deadline time.Time
	if c.RetryWindow > 0 {
		deadline = time.Now().Add(c.RetryWindow)
	}
	wait := c.RetryBackoff
	if wait <= 0 {
		wait = 250 * time.Millisecond
	}
	for {
		status, out, err := c.attempt(ctx, method, path, body, limit)
		if err == nil {
			return status, out, nil
		}
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		left := time.Until(deadline)
		if deadline.IsZero() || left <= 0 || !IsTransient(err) || !idempotent(method, path) && !notSent(err) {
			return status, nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		t := time.NewTimer(min(wait, left))
		select {
		case <-ctx.Done():
			t.Stop()
			return 0, nil, ctx.Err()
		case <-t.C:
		}
		wait = min(wait*2, 5*time.Second)
	}
}

func (c *Client) attempt(ctx context.Context, method, path string, body []byte, limit int64) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("reading response: %w", err)
	}
	return resp.StatusCode, out, nil
}

func (c *Client) roundTrip(ctx context.Context, method, path string, limit int64) (int, []byte, error) {
	status, body, err := c.exchange(ctx, method, path, nil, limit)
	if err != nil {
		return status, nil, err
	}
	switch {
	case status == http.StatusNotModified: // already started / stopped
		return status, nil, nil
	case status == http.StatusNotFound:
		return status, nil, apperr.New(http.StatusNotFound, "not_found", "no such container")
	case status == http.StatusForbidden || status == http.StatusMethodNotAllowed:
		// The socket proxy refused the call: its allowlist is narrower.
		return status, nil, apperr.New(http.StatusBadGateway, "docker_forbidden",
			"the Docker socket proxy refused "+method+" "+strings.SplitN(path, "?", 2)[0]+"; check its allowlist")
	case status >= 400:
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Message == "" {
			e.Message = http.StatusText(status)
		}
		return status, nil, apperr.New(http.StatusBadGateway, "docker_error", "Docker: "+e.Message)
	}
	return status, body, nil
}

func (c *Client) getJSON(ctx context.Context, op, path string, out any) error {
	body, err := c.request(ctx, op, http.MethodGet, path, 8<<20)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: decoding %s: %v", ErrUnavailable, op, err)
	}
	return nil
}

func (c *Client) List(ctx context.Context, project string) ([]Container, error) {
	f, _ := json.Marshal(map[string][]string{"label": {LabelProject + "=" + project}})
	var out []Container
	if err := c.getJSON(ctx, "list", "/containers/json?all=1&filters="+url.QueryEscape(string(f)), &out); err != nil {
		return nil, err
	}
	// The filter is the proxy's job too, but never trust it alone.
	kept := out[:0]
	for _, ct := range out {
		if ct.Labels[LabelProject] == project && ct.Labels[LabelOneOff] != "True" {
			kept = append(kept, ct)
		}
	}
	return kept, nil
}

func idPath(id string) string { return "/containers/" + url.PathEscape(id) }

func (c *Client) Inspect(ctx context.Context, id string) (*Inspect, error) {
	var out Inspect
	if err := c.getJSON(ctx, "inspect", idPath(id)+"/json", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Ping asks GET /_ping (both of the stack's proxies allow it).
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.request(ctx, "ping", http.MethodGet, "/_ping", 4<<10)
	return err
}

// Info reads GET /info (the proxy must allow it; an older stack's doesn't,
// which is an error here).
func (c *Client) Info(ctx context.Context) (*Info, error) {
	var out Info
	if err := c.getJSON(ctx, "info", "/info", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) post(ctx context.Context, op, path string) error {
	_, err := c.request(ctx, op, http.MethodPost, path, 64<<10)
	return err
}

// Stop and restart give the container 10 s to exit (compose's default).
func (c *Client) Start(ctx context.Context, id string) error {
	return c.post(ctx, "start", idPath(id)+"/start")
}
func (c *Client) Stop(ctx context.Context, id string) error {
	return c.post(ctx, "stop", idPath(id)+"/stop?t=10")
}
func (c *Client) Restart(ctx context.Context, id string) error {
	return c.post(ctx, "restart", idPath(id)+"/restart?t=10")
}

func (c *Client) Logs(ctx context.Context, id string, o LogOptions) ([]LogLine, error) {
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "timestamps": {"1"}}
	if o.Tail > 0 {
		q.Set("tail", strconv.Itoa(o.Tail))
	} else {
		q.Set("tail", "all")
	}
	if !o.Since.IsZero() {
		// Docker's since is inclusive with nanosecond precision; ask from the
		// next nanosecond so the last line already seen isn't repeated.
		t := o.Since.Add(time.Nanosecond)
		q.Set("since", fmt.Sprintf("%d.%09d", t.Unix(), t.Nanosecond()))
	}
	body, err := c.request(ctx, "logs", http.MethodGet, idPath(id)+"/logs?"+q.Encode(), c.MaxLogBytes)
	if err != nil {
		return nil, err
	}
	return ParseLogs(body, o.Tty), nil
}

// ParseLogs splits a logs response into lines. Without a TTY Docker
// multiplexes stdout and stderr in frames: 1 byte stream, 3 zero bytes, a
// big-endian uint32 length, then the payload.
func ParseLogs(body []byte, tty bool) []LogLine {
	var out []LogLine
	add := func(stream string, payload []byte) {
		sc := bufio.NewScanner(bytes.NewReader(payload))
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			out = append(out, parseLine(stream, sc.Text()))
		}
	}
	if tty || !multiplexed(body) {
		add("stdout", body)
		return out
	}
	// Frames can split a line; join each stream's payload in order.
	var cur bytes.Buffer
	curStream := ""
	for len(body) >= 8 {
		stream := "stdout"
		if body[0] == 2 {
			stream = "stderr"
		}
		n := int(binary.BigEndian.Uint32(body[4:8]))
		body = body[8:]
		if n > len(body) {
			n = len(body)
		}
		if stream != curStream && cur.Len() > 0 {
			add(curStream, cur.Bytes())
			cur.Reset()
		}
		curStream = stream
		cur.Write(body[:n])
		body = body[n:]
	}
	if cur.Len() > 0 {
		add(curStream, cur.Bytes())
	}
	return out
}

func multiplexed(b []byte) bool {
	return len(b) >= 8 && (b[0] == 0 || b[0] == 1 || b[0] == 2) && b[1] == 0 && b[2] == 0 && b[3] == 0
}

func parseLine(stream, s string) LogLine {
	l := LogLine{Stream: stream, Text: s}
	if ts, rest, ok := strings.Cut(s, " "); ok {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			l.At, l.Text = t, rest
		}
	}
	l.Text = strings.TrimRight(l.Text, "\r")
	return l
}

type rawStats struct {
	CPU struct {
		Usage struct {
			Total uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		System uint64 `json:"system_cpu_usage"`
		Online uint32 `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPU struct {
		Usage struct {
			Total uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		System uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	Memory struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
}

// Stats takes one sample. Docker waits for a second CPU reading before
// answering (about a second), so callers run these in parallel.
func (c *Client) Stats(ctx context.Context, id string) (*Stats, error) {
	var r rawStats
	if err := c.getJSON(ctx, "stats", idPath(id)+"/stats?stream=false", &r); err != nil {
		return nil, err
	}
	return computeStats(r), nil
}

func computeStats(r rawStats) *Stats {
	s := &Stats{MemoryLimit: r.Memory.Limit}
	// Like `docker stats`: usage minus page cache (cgroup v2 inactive_file,
	// v1 total_inactive_file).
	mem := r.Memory.Usage
	cache := r.Memory.Stats["inactive_file"]
	if cache == 0 {
		cache = r.Memory.Stats["total_inactive_file"]
	}
	if cache < mem {
		mem -= cache
	}
	s.MemoryBytes = mem
	cpuDelta := float64(r.CPU.Usage.Total) - float64(r.PreCPU.Usage.Total)
	sysDelta := float64(r.CPU.System) - float64(r.PreCPU.System)
	if cpuDelta > 0 && sysDelta > 0 {
		online := float64(r.CPU.Online)
		if online == 0 {
			online = 1
		}
		s.CPUPercent = cpuDelta / sysDelta * online * 100
	}
	return s
}

// IsUnavailable reports whether err means the proxy is down or refusing.
func IsUnavailable(err error) bool {
	if errors.Is(err, ErrUnavailable) {
		return true
	}
	e, ok := apperr.As(err)
	return ok && (e.Code == "docker_unavailable" || e.Code == "docker_forbidden")
}

// Helper jobs (updates): a short-lived container that runs one shell script
// with the Docker socket and the stack directory mounted. These calls need a
// wider allowlist (POST /images/create, POST /containers/create, DELETE
// /containers/{id}), which the stack only grants on a separate proxy (its
// updates profile).

// JobSpec is the container a helper job runs in.
type JobSpec struct {
	Name   string
	Image  string
	Cmd    []string
	Env    []string
	Labels map[string]string
	// Binds are host:container[:mode] mounts.
	Binds      []string
	WorkingDir string
	// NetworkMode: "none" (the default) keeps the helper off every network;
	// it only speaks to the Docker daemon through the mounted socket.
	NetworkMode string
}

// JobsClientImpl starts and inspects helper containers.
type JobsClientImpl interface {
	DockerClientImpl
	// Pull fetches an image (POST /images/create), reading the progress stream to the end.
	Pull(ctx context.Context, image string) error
	// Create creates a container and returns its ID.
	Create(ctx context.Context, spec JobSpec) (string, error)
	// Remove deletes a container (force: even if it runs).
	Remove(ctx context.Context, id string, force bool) error
	// ListLabel lists containers (all states) with a label (key or key=value).
	ListLabel(ctx context.Context, label string) ([]Container, error)
}

// NewJobsClient talks to a proxy that allows helper jobs.
func NewJobsClient(baseURL string, timeout time.Duration, observe Observer, opts ...Option) JobsClientImpl {
	return newClient(baseURL, timeout, observe, opts)
}

func (c *Client) send(ctx context.Context, op, method, path string, body any, limit int64) ([]byte, error) {
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	start := time.Now()
	status, out, err := c.exchange(ctx, method, path, raw, limit)
	defer func() {
		if c.Observe != nil {
			c.Observe(op, status, err, time.Since(start))
		}
	}()
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusForbidden || status == http.StatusMethodNotAllowed:
		err = apperr.New(http.StatusBadGateway, "docker_forbidden",
			"the Docker socket proxy refused "+method+" "+strings.SplitN(path, "?", 2)[0]+"; check its allowlist (the stack's updates profile)")
		return nil, err
	case status == http.StatusNotFound:
		err = apperr.New(http.StatusNotFound, "not_found", "Docker: "+dockerMessage(out, "not found"))
		return nil, err
	case status >= 400:
		err = apperr.New(http.StatusBadGateway, "docker_error", "Docker: "+dockerMessage(out, http.StatusText(status)))
		return nil, err
	}
	return out, nil
}

func dockerMessage(body []byte, def string) string {
	var e struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &e)
	if e.Message == "" {
		return def
	}
	return e.Message
}

func (c *Client) Pull(ctx context.Context, image string) error {
	ref, tag := image, ""
	// repo[:tag][@digest]: split the tag off the last path segment only.
	if i := strings.LastIndex(ref, "/"); !strings.Contains(ref[i+1:], "@") {
		if j := strings.LastIndex(ref[i+1:], ":"); j >= 0 {
			ref, tag = image[:i+1+j], image[i+2+j:]
		}
	}
	q := url.Values{"fromImage": {ref}}
	if tag != "" {
		q.Set("tag", tag)
	}
	body, err := c.send(ctx, "pull", http.MethodPost, "/images/create?"+q.Encode(), nil, 32<<20)
	if err != nil {
		return err
	}
	// The stream is JSON lines; an error is a line with "error".
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var l struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.Error != "" {
			return apperr.New(http.StatusBadGateway, "docker_error", "Docker: pulling "+image+": "+l.Error)
		}
	}
	return nil
}

func (c *Client) Create(ctx context.Context, s JobSpec) (string, error) {
	nm := s.NetworkMode
	if nm == "" {
		nm = "none"
	}
	body := map[string]any{
		"Image": s.Image, "Cmd": s.Cmd, "Env": s.Env, "Labels": s.Labels, "WorkingDir": s.WorkingDir,
		"Tty": false, "AttachStdout": false, "AttachStderr": false,
		"HostConfig": map[string]any{"Binds": s.Binds, "NetworkMode": nm, "AutoRemove": false,
			"LogConfig": map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "10m"}}},
	}
	out, err := c.send(ctx, "create", http.MethodPost, "/containers/create?name="+url.QueryEscape(s.Name), body, 64<<10)
	if err != nil {
		return "", err
	}
	var r struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(out, &r); err != nil || r.ID == "" {
		return "", fmt.Errorf("%w: decoding create: %v", ErrUnavailable, err)
	}
	return r.ID, nil
}

func (c *Client) Remove(ctx context.Context, id string, force bool) error {
	q := "?v=1"
	if force {
		q += "&force=1"
	}
	_, err := c.send(ctx, "remove", http.MethodDelete, idPath(id)+q, nil, 64<<10)
	return err
}

func (c *Client) ListLabel(ctx context.Context, label string) ([]Container, error) {
	f, _ := json.Marshal(map[string][]string{"label": {label}})
	var out []Container
	if err := c.getJSON(ctx, "list", "/containers/json?all=1&filters="+url.QueryEscape(string(f)), &out); err != nil {
		return nil, err
	}
	k, v, hasV := strings.Cut(label, "=")
	kept := out[:0]
	for _, ct := range out {
		if got, ok := ct.Labels[k]; ok && (!hasV || got == v) {
			kept = append(kept, ct)
		}
	}
	return kept, nil
}
