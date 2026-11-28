package updates_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boson-chat/admin/internal/services/updates"
)

// The recreate helper brings the socket proxies (LAST) up after everything
// else: while compose replaces one the panel can't see Docker, so nothing
// else may still be changing then.
func TestRecreateScriptProxiesLast(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	stack := filepath.Join(dir, "stack")
	for _, d := range []string{bin, stack} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	calls := filepath.Join(dir, "calls")
	stub := "#!/bin/sh\necho \"$*\" >> " + calls + "\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(stack, ".env")
	if err := os.WriteFile(envFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	loc := &updates.Location{Project: "t", Dir: stack, EnvFile: envFile, ComposeFiles: []string{filepath.Join(stack, "compose.yaml")}}
	env := updates.RollbackEnv(loc, filepath.Join(stack, "backups"), filepath.Join(stack, "backups", "r"),
		"SERVICES", "auth ergo", "FORCE", "traefik", "LAST", "docker-proxy docker-proxy-updates")
	cmd := exec.Command("sh", "-c", updates.ScriptRecreate)
	cmd.Env = []string{"PATH=" + bin + ":" + os.Getenv("PATH")}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "@@RESULT recreate") {
		t.Fatalf("recreate: %v\n%s", err, out)
	}
	b, _ := os.ReadFile(calls)
	var ups []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		_, rest, _ := strings.Cut(l, " up -d --no-deps ")
		ups = append(ups, rest)
	}
	want := []string{"auth ergo", "--force-recreate traefik", "docker-proxy docker-proxy-updates"}
	if strings.Join(ups, "|") != strings.Join(want, "|") {
		t.Fatalf("compose up calls %q, want %q\n%s", ups, want, b)
	}
}
