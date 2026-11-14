package dockerapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

func frame(stream byte, s string) []byte {
	h := make([]byte, 8)
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], uint32(len(s)))
	return append(h, s...)
}

func TestParseLogsMultiplexed(t *testing.T) {
	var b bytes.Buffer
	b.Write(frame(1, "2026-11-13T10:00:00.000000001Z hello\n2026-11-13T10:00:01Z sp"))
	b.Write(frame(1, "lit line\n"))
	b.Write(frame(2, "2026-11-13T10:00:02Z oops\r\n"))
	lines := ParseLogs(b.Bytes(), false)
	if len(lines) != 3 {
		t.Fatalf("lines: %+v", lines)
	}
	if lines[0].Text != "hello" || lines[0].At.Nanosecond() != 1 || lines[1].Text != "split line" || lines[2].Stream != "stderr" || lines[2].Text != "oops" {
		t.Fatalf("parsed: %+v", lines)
	}
	tty := ParseLogs([]byte("2026-11-13T10:00:00Z a\nno timestamp\n"), true)
	if len(tty) != 2 || tty[0].Text != "a" || tty[1].Text != "no timestamp" || !tty[1].At.IsZero() {
		t.Fatalf("tty: %+v", tty)
	}
}

func TestComputeStats(t *testing.T) {
	var r rawStats
	r.CPU.Usage.Total, r.PreCPU.Usage.Total = 2_000_000, 1_000_000
	r.CPU.System, r.PreCPU.System = 20_000_000, 10_000_000
	r.CPU.Online = 4
	r.Memory.Usage, r.Memory.Limit = 100<<20, 1<<30
	r.Memory.Stats = map[string]uint64{"inactive_file": 30 << 20}
	s := computeStats(r)
	if s.CPUPercent != 40 || s.MemoryBytes != 70<<20 || s.MemoryLimit != 1<<30 {
		t.Fatalf("stats %+v", s)
	}
}

func TestClient(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.URL.Path == "/containers/json":
			if !strings.Contains(r.URL.Query().Get("filters"), "com.docker.compose.project=boson") {
				t.Errorf("filters %q", r.URL.Query().Get("filters"))
			}
			// A proxy that ignores the filter: the client drops strangers.
			_, _ = w.Write([]byte(`[
				{"Id":"a1","Names":["/boson-auth-1"],"Labels":{"com.docker.compose.project":"boson","com.docker.compose.service":"auth"},"State":"running"},
				{"Id":"b2","Names":["/other"],"Labels":{"com.docker.compose.project":"other"}},
				{"Id":"c3","Names":["/boson-auth-run-1"],"Labels":{"com.docker.compose.project":"boson","com.docker.compose.oneoff":"True"}}]`))
		case r.URL.Path == "/containers/a1/stop":
			if r.Method != "POST" || r.URL.Query().Get("t") != "10" {
				t.Errorf("stop %s %s", r.Method, r.URL)
			}
			w.WriteHeader(204)
		case r.URL.Path == "/containers/a1/start":
			w.WriteHeader(304)
		case r.URL.Path == "/containers/a1/restart":
			w.WriteHeader(403) // proxy refusal
			_, _ = w.Write([]byte("Forbidden"))
		case r.URL.Path == "/containers/a1/logs":
			if r.URL.Query().Get("since") != "1763028000.000000001" || r.URL.Query().Get("tail") != "50" {
				t.Errorf("logs query %v", r.URL.Query())
			}
			_, _ = w.Write(frame(1, "2026-11-13T10:00:01Z x\n"))
		case r.URL.Path == "/containers/zz/json":
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"No such container: zz"}`))
		default:
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		}
	}))
	defer srv.Close()
	c := NewDockerClient(srv.URL, time.Second, nil)
	ctx := context.Background()
	list, err := c.List(ctx, "boson")
	if err != nil || len(list) != 1 || list[0].Name() != "boson-auth-1" {
		t.Fatalf("list: %v %+v", err, list)
	}
	if err := c.Stop(ctx, "a1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(ctx, "a1"); err != nil {
		t.Fatalf("304 is fine: %v", err)
	}
	if err := c.Restart(ctx, "a1"); !IsUnavailable(err) || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("refused: %v", err)
	}
	lines, err := c.Logs(ctx, "a1", LogOptions{Tail: 50, Since: time.Unix(1763028000, 0)})
	if err != nil || len(lines) != 1 || lines[0].Text != "x" {
		t.Fatalf("logs: %v %+v", err, lines)
	}
	if _, err := c.Inspect(ctx, "zz"); err == nil {
		t.Fatal("missing container")
	} else if e, _ := apperr.As(err); e.Status != 404 {
		t.Fatalf("404: %v", err)
	}
	if _, err := c.Stats(ctx, "a1"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("500: %v", err)
	}
	if _, err := NewDockerClient("http://127.0.0.1:1", 200*time.Millisecond, nil).List(ctx, "boson"); !IsUnavailable(err) {
		t.Fatalf("unreachable: %v", err)
	}
}
