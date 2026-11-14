package dockerapi

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestAgainstProxy runs against a real socket proxy when
// DOCKER_PROXY_TEST_URL is set, with a container named by
// DOCKER_PROXY_TEST_CONTAINER in compose project DOCKER_PROXY_TEST_PROJECT
// that writes to stdout and stderr. See README "Services control".
func TestAgainstProxy(t *testing.T) {
	base := os.Getenv("DOCKER_PROXY_TEST_URL")
	if base == "" {
		t.Skip("DOCKER_PROXY_TEST_URL not set")
	}
	project, name := os.Getenv("DOCKER_PROXY_TEST_PROJECT"), os.Getenv("DOCKER_PROXY_TEST_CONTAINER")
	c := NewDockerClient(base, 20*time.Second, nil)
	ctx := context.Background()
	list, err := c.List(ctx, project)
	if err != nil || len(list) == 0 {
		t.Fatalf("list: %v %+v", err, list)
	}
	var id string
	for _, ct := range list {
		if ct.Name() == name {
			id = ct.ID
		}
	}
	if id == "" {
		t.Fatalf("%s not in %s", name, project)
	}
	in, err := c.Inspect(ctx, id)
	if err != nil || !in.State.Running {
		t.Fatalf("inspect: %v %+v", err, in)
	}
	lines, err := c.Logs(ctx, id, LogOptions{Tail: 6, Tty: in.Config.Tty})
	if err != nil || len(lines) == 0 {
		t.Fatalf("logs: %v", err)
	}
	streams := map[string]bool{}
	for _, l := range lines {
		streams[l.Stream] = true
		if l.At.IsZero() {
			t.Fatalf("no timestamp: %+v", l)
		}
	}
	if !streams["stderr"] || !streams["stdout"] {
		t.Fatalf("streams not demultiplexed: %+v", lines)
	}
	last := lines[len(lines)-1].At
	time.Sleep(1500 * time.Millisecond)
	newer, err := c.Logs(ctx, id, LogOptions{Since: last, Tty: in.Config.Tty})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range newer {
		if !l.At.After(last) {
			t.Fatalf("since repeated a line: %+v (last %v)", l, last)
		}
	}
	st, err := c.Stats(ctx, id)
	if err != nil || st.MemoryBytes == 0 {
		t.Fatalf("stats: %v %+v", err, st)
	}
	if err := c.Restart(ctx, id); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if err := c.Stop(ctx, id); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := c.Start(ctx, id); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Logf("%d lines, %d newer, cpu %.2f%% mem %d", len(lines), len(newer), st.CPUPercent, st.MemoryBytes)
}
