package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/boson-chat/admin/internal/services/telemetry"
)

func TestTelemetrySwitchAndPreview(t *testing.T) {
	h := newHarness(t)
	path := h.withConfigFile("version: 1\n")
	c, csrf := h.login()

	w := h.do("GET", "/api/telemetry", nil, withCookie(c))
	var st struct {
		Enabled, Locked, FileOn, ConfigFile bool
		Endpoint                            string
	}
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":true`) || !strings.Contains(w.Body.String(), `"config_file":true`) {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}

	w = h.do("GET", "/api/telemetry/preview", nil, withCookie(c))
	var r telemetry.Report
	if err := json.Unmarshal(w.Body.Bytes(), &r); w.Code != 200 || err != nil || r.Schema != 1 || len(r.InstallID) != 32 {
		t.Fatalf("preview: %d %s", w.Code, w.Body)
	}

	if w := h.do("PUT", "/api/telemetry", map[string]any{"enabled": false}, withCookie(c)); w.Code != http.StatusForbidden {
		t.Fatalf("no CSRF: %d", w.Code)
	}
	if w := h.do("PUT", "/api/telemetry", map[string]any{}, withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 400 {
		t.Fatalf("no enabled: %d", w.Code)
	}
	w = h.do("PUT", "/api/telemetry", map[string]any{"enabled": false}, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("off: %d %s", w.Code, w.Body)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "telemetry: false") {
		t.Fatalf("file:\n%s", b)
	}
	w = h.do("PUT", "/api/telemetry", map[string]any{"enabled": true}, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatalf("on: %d %s", w.Code, w.Body)
	}
}

func TestTelemetryLockedByEnv(t *testing.T) {
	h := newHarness(t)
	h.withConfigFile("version: 1\n")
	h.h.Telemetry = telemetry.New(telemetry.Options{EnvOff: true})
	c, csrf := h.login()
	w := h.do("GET", "/api/telemetry", nil, withCookie(c))
	if !strings.Contains(w.Body.String(), `"locked":true`) || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("status: %s", w.Body)
	}
	w = h.do("PUT", "/api/telemetry", map[string]any{"enabled": true}, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "telemetry_locked") {
		t.Fatalf("locked: %d %s", w.Code, w.Body)
	}
}
