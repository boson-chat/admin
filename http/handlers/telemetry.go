package handlers

import (
	"net/http"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/services/stackconfig"
	"github.com/boson-chat/admin/internal/services/telemetry"
	"github.com/boson-chat/admin/internal/session"
)

var errEnabledMissing = apperr.Invalid("enabled (true or false) is required")

// telemetryView is GET /api/telemetry: the usage & health stats switch,
// the last send and the next one.
type telemetryView struct {
	*telemetry.Status
	// ConfigFile: the switch can be saved (BOSON_CONFIG is set).
	ConfigFile bool `json:"config_file"`
}

func (h *Handlers) telemetryView() telemetryView {
	return telemetryView{Status: h.Telemetry.Status(), ConfigFile: h.Config.Enabled()}
}

func (h *Handlers) telemetryStatus(w http.ResponseWriter, r *http.Request, _ *session.Session) {
	writeJSON(w, http.StatusOK, h.telemetryView())
}

// telemetryPreview is the next report exactly as it would be sent.
func (h *Handlers) telemetryPreview(w http.ResponseWriter, r *http.Request, _ *session.Session) {
	writeJSON(w, http.StatusOK, h.Telemetry.Preview(r.Context()))
}

// telemetrySet is PUT /api/telemetry {"enabled": bool}: admin.telemetry
// in config/boson.yaml.
func (h *Handlers) telemetrySet(w http.ResponseWriter, r *http.Request, s *session.Session) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decode(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	if in.Enabled == nil {
		writeError(w, r, errEnabledMissing)
		return
	}
	if h.Telemetry.Locked() && *in.Enabled {
		writeError(w, r, telemetry.ErrLocked)
		return
	}
	c := stackconfig.Change{Set: map[string]any{stackconfig.KeyTelemetry: *in.Enabled}}
	if _, err := h.Config.Update(r.Context(), s.Snapshot().Username, "", c, h.can(r, s)); err != nil {
		writeError(w, r, configErr(err))
		return
	}
	h.Telemetry.Kick()
	writeJSON(w, http.StatusOK, h.telemetryView())
}
