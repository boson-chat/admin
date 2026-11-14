package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeKeepsExistingAndComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boson.yaml")
	_ = os.WriteFile(path, []byte("version: 1\n# mine\nauth:\n  registration: invite\n\n# reference below\n# voice:\n"), 0o644)
	in := "auth:\n  registration: open\n  require_email_verification: true\n---\nvoice:\n  rooms:\n    - {id: general, name: General}\n"
	added, kept, err := mergeInto(path, []byte(in), false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(added, ",") != "auth.require_email_verification,voice.rooms" || strings.Join(kept, ",") != "auth.registration" {
		t.Fatalf("added %v kept %v", added, kept)
	}
	b, _ := os.ReadFile(path)
	out := string(b)
	for _, want := range []string{"# mine", "registration: invite", "require_email_verification: true", "- {id: general, name: General}", "# reference below", "by migrate-config at"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if _, _, err := mergeInto(path, []byte("auth:\n  registration: open\n"), true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "registration: open") {
		t.Fatalf("--overwrite:\n%s", b)
	}
}

func TestExportAdmin(t *testing.T) {
	env := map[string]string{"DIRECTORY_REGISTRATION": "false", "TLS_CHECK_HOSTS": "a.example.org:443, b.example.org:6697@ergo:6697"}
	var buf bytes.Buffer
	if err := exportAdmin(&buf, func(k string) (string, bool) { v, ok := env[k]; return v, ok }); err != nil {
		t.Fatal(err)
	}
	want := "admin:\n  directory_registration: false\n  tls_check_hosts:\n    - a.example.org:443\n    - b.example.org:6697@ergo:6697\n"
	if buf.String() != want {
		t.Fatalf("got:\n%s", buf.String())
	}
	env["TLS_CHECK_HOSTS"] = "nope"
	if err := exportAdmin(&buf, func(k string) (string, bool) { v, ok := env[k]; return v, ok }); err == nil {
		t.Fatal("bad env exported")
	}
}
