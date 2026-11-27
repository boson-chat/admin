package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/config"
	"github.com/boson-chat/admin/http/handlers"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/configfile"
	"github.com/boson-chat/admin/internal/services/stackconfig"
)

// The report the panel builds from an env and a config file full of
// secrets and names, through the real wiring, contains none of them.
func TestTelemetryReportHasNoSecrets(t *testing.T) {
	secrets := []string{
		"c2Vzc2lvbi1rZXktdGhhdC1pcy1zZWNyZXQtMTIzNA==", "internal-api-token-9f8e", "api-internal-tok-77",
		"files-internal-tok-66", "directory-internal-tok-55", "directory-key-44", "ghp_updates_token_33",
		"smtp-pass-swordfish", "postgres-pass-hunter2", "sso-client-secret-zz", "sso-client-id-yy",
		"example-secret.org", "irc.example-secret.org", "auth.example-secret.org", "voice.example-secret.org",
		"api.example-secret.org", "files.example-secret.org", "web.example-secret.org", "video.example-secret.org",
		"owner@example-secret.org", "Secret Network", "secret-net-project", "Corp IdP", "corp-idp",
		"#secret-lobby", "203.0.113.9",
	}
	auth := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.URL.Path == "/v1/config" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"registration": "open", "email": map[string]any{"configured": true},
				"sso_providers": []any{map[string]any{"slug": "corp-idp", "kind": "oidc", "display_name": "Corp IdP",
					"client_id": "sso-client-id-yy", "client_secret": "sso-client-secret-zz"}},
			})
			return
		}
		nethttp.NotFound(w, r)
	}))
	defer auth.Close()
	docker := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		labels := map[string]string{"com.docker.compose.project": "secret-net-project", "com.docker.compose.service": "auth",
			"com.docker.compose.project.working_dir": "/home/owner/example-secret.org"}
		if r.URL.Path == "/containers/json" {
			_ = json.NewEncoder(w).Encode([]map[string]any{{"Id": "c1", "Names": []string{"/auth.example-secret.org"},
				"Image": "registry.example-secret.org/boson/auth:1.9.2", "State": "running", "Labels": labels}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": "c1", "Name": "/auth.example-secret.org", "RestartCount": 1,
			"State": map[string]any{"Status": "running", "Running": true}, "Config": map[string]any{"Labels": labels,
				"Env": []string{"SMTP_PASSWORD=smtp-pass-swordfish"}}})
	}))
	defer docker.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "boson.yaml")
	yaml := `version: 1
network:
  name: Secret Network
auth:
  registration: open
  smtp:
    host: smtp.example-secret.org
    from: owner@example-secret.org
  federation:
    allow: [example-secret.org]
irc:
  channels:
    - name: "#secret-lobby"
admin:
  directory_hostname: irc.example-secret.org
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	store := configfile.Open(path, configfile.Options{Header: stackconfig.FileHeader})
	stack := stackconfig.New(stackconfig.Options{Store: store})

	var cfg config.Config
	cfg.AppConfig.Version = "1.32.0"
	cfg.SessionConfig.Key = secrets[0]
	cfg.UpstreamConfig = config.UpstreamConfig{AuthURL: auth.URL, InternalAPIToken: secrets[1],
		AuthHost: "auth.example-secret.org", VoiceHost: "voice.example-secret.org", IRCHost: "irc.example-secret.org",
		APIHost: "api.example-secret.org", FilesHost: "files.example-secret.org", WebHost: "web.example-secret.org",
		VideoHost: "video.example-secret.org", CertHosts: "irc.example-secret.org,203.0.113.9", Timeout: 2 * time.Second}
	cfg.BosonConfig = config.BosonConfig{APIInternalToken: secrets[2], FilesInternalToken: secrets[3],
		DirectoryInternalToken: secrets[4], DirectoryKey: secrets[5], Domain: "example-secret.org"}
	cfg.UpdatesConfig.GitHubToken = secrets[6]
	cfg.DockerConfig = config.DockerConfig{ProxyURL: docker.URL, Project: "secret-net-project", Timeout: 2 * time.Second}
	cfg.UsageStats = config.UsageStatsConfig{Dir: t.TempDir(), URL: "http://127.0.0.1:1"}
	t.Setenv("POSTGRES_PASSWORD", "postgres-pass-hunter2")

	h := &handlers.Handlers{Config: stack}
	ac := authapi.NewAuthClient(auth.URL, 2*time.Second, nil)
	tel := buildTelemetry(cfg, h, stack, ac, nil, nil, nil, func(string) func(string, int, error, time.Duration) { return nil })
	b, err := json.Marshal(tel.Preview(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	low := strings.ToLower(string(b))
	for _, s := range secrets {
		if strings.Contains(low, strings.ToLower(s)) {
			t.Errorf("the report contains %q:\n%s", s, b)
		}
	}
	// It did read the sources (so the test isn't vacuous).
	for _, want := range []string{`"auth":"1.9.2"`, `"sso":["oidc"]`, `"smtp":true`, `"federation_peers":"1"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("report lacks %s:\n%s", want, b)
		}
	}
}
