package updates_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boson-chat/admin/internal/services/updates"
)

// TestStackRollbackPutsBackTheMigrationRecord runs the rollback helper
// script with a docker that does nothing, in the shape of the ircforever
// install (2026-11-27): its first stack update (a pre-release install, no
// .boson/migrations.applied yet) ran 0002-wellknown-web.sh, which added web
// to WELLKNOWN_SECTIONS, and was rolled back. The rollback put back .env
// from the backup but left the update's migrations.applied, so every later
// update skipped 0002 and boson.json lost its web client. The records the
// update created go too; those the backup has come back as they were.
func TestStackRollbackPutsBackTheMigrationRecord(t *testing.T) {
	for _, tc := range []struct {
		name    string
		before  string // migrations.applied before the update ("" = none)
		wantRec bool
	}{
		{"first stack update", "", false},
		{"later stack update", "0001-bridge-client-webirc.sh 2026-11-27T16:20:00Z 1.0.1\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			stack := filepath.Join(dir, "stack")
			bdir := filepath.Join(stack, "backups", "updates", "u1")
			for _, d := range []string{bin, filepath.Join(stack, ".boson"), bdir} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write := func(p, s string) {
				t.Helper()
				if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			// docker: `compose config --services` lists nothing; nothing else is asked.
			write(filepath.Join(bin, "docker"), "#!/bin/sh\nexit 0\n")
			if err := os.Chmod(filepath.Join(bin, "docker"), 0o755); err != nil {
				t.Fatal(err)
			}
			oldEnv := "COMPOSE_PROFILES=auth,bridge,web\nWEB_HOST=chat.x\nWELLKNOWN_SECTIONS=irc,auth,voice,files\n"
			envFile := filepath.Join(stack, ".env")
			write(envFile, oldEnv)
			write(filepath.Join(stack, "compose.yaml"), "services: {}\n")
			if tc.before != "" {
				write(filepath.Join(stack, ".boson", "migrations.applied"), tc.before)
				write(filepath.Join(stack, ".boson", "stack-files.sha256"), "old  compose.yaml\n")
			}
			// The backup, as scriptBackup makes it.
			write(filepath.Join(bdir, "env"), oldEnv)
			tar := exec.Command("tar", "czf", filepath.Join(bdir, "stack-dir.tgz"), "-C", stack, "--exclude=./backups", ".")
			if out, err := tar.CombinedOutput(); err != nil {
				t.Fatalf("tar: %v\n%s", err, out)
			}
			// The update: files, the new records, the migration.
			write(envFile, strings.Replace(oldEnv, "files\n", "files,web\n", 1))
			write(filepath.Join(stack, ".boson", "migrations.applied"),
				tc.before+"0002-wellknown-web.sh 2026-11-27T17:12:27Z 1.0.1\n")
			write(filepath.Join(stack, ".boson", "stack-files.sha256"), "new  compose.yaml\n")
			write(filepath.Join(stack, ".boson", "manifest.json"), `{"version":"1.0.1"}`)
			write(filepath.Join(stack, "VERSION"), "1.0.1\n")
			write(filepath.Join(bdir, "stack-added.txt"), "VERSION\n")

			loc := &updates.Location{Project: "t", Dir: stack, EnvFile: envFile, ComposeFiles: []string{filepath.Join(stack, "compose.yaml")}}
			env := updates.RollbackEnv(loc, filepath.Join(stack, "backups", "updates"), bdir, "STACK_RESTORE", "1")
			cmd := exec.Command("sh", "-c", updates.ScriptRollback)
			cmd.Env = []string{"PATH=" + bin + ":" + os.Getenv("PATH")}
			for k, v := range env {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("rollback: %v\n%s", err, out)
			}

			if b, _ := os.ReadFile(envFile); string(b) != oldEnv {
				t.Fatalf(".env = %q, want the backup's", b)
			}
			rec, err := os.ReadFile(filepath.Join(stack, ".boson", "migrations.applied"))
			if !tc.wantRec {
				if err == nil {
					t.Fatalf("migrations.applied kept from the rolled-back update: %q", rec)
				}
			} else if string(rec) != tc.before {
				t.Fatalf("migrations.applied = %q, want the backup's %q", rec, tc.before)
			}
			for _, f := range []string{"manifest.json", "VERSION"} {
				p := filepath.Join(stack, ".boson", f)
				if f == "VERSION" {
					p = filepath.Join(stack, f)
				}
				if _, err := os.Stat(p); err == nil {
					t.Errorf("%s left from the rolled-back update", f)
				}
			}
			sums, _ := os.ReadFile(filepath.Join(stack, ".boson", "stack-files.sha256"))
			if want := map[bool]string{true: "old  compose.yaml\n", false: ""}[tc.wantRec]; string(sums) != want {
				t.Errorf("stack-files.sha256 = %q, want %q", sums, want)
			}
		})
	}
}
