package handlers

import (
	"encoding/json"
	"testing"

	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/clients/authapi/authapitest"
)

func TestRoleChannelsEndpoints(t *testing.T) {
	h := newHarness(t)
	c, csrf := h.login()
	auth := []reqOpt{withCookie(c), withHeader(CSRFHeader, csrf)}

	w := h.do("PUT", "/api/roles/"+authapitest.RoleModerator+"/channels", map[string]any{
		"channels": []map[string]any{{"channel": "#Staff", "autojoin": true}, {"channel": "#mods", "autojoin": false}},
	}, auth...)
	if w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if got := h.fake.RoleChans[authapitest.RoleModerator]; len(got) != 2 || got[1] != (authapi.RoleChannel{Channel: "#staff", Autojoin: true}) {
		t.Fatalf("saved %+v", got)
	}
	// a missing list is refused; a bad name is a 400 naming it
	if w := h.do("PUT", "/api/roles/everyone/channels", map[string]any{}, auth...); w.Code != 400 {
		t.Fatalf("no channels: %d %s", w.Code, w.Body)
	}
	if w := h.do("PUT", "/api/roles/everyone/channels", map[string]any{"channels": []map[string]any{{"channel": "lobby"}}}, auth...); w.Code != 400 {
		t.Fatalf("bad name: %d %s", w.Code, w.Body)
	}
	if w := h.do("PUT", "/api/channel-access", map[string]any{"channels": []map[string]any{{"channel": "#staff", "private": true}}}, auth...); w.Code != 200 {
		t.Fatalf("access: %d %s", w.Code, w.Body)
	}

	w = h.do("GET", "/api/channels", nil, auth...)
	if w.Code != 200 {
		t.Fatalf("overview: %d %s", w.Code, w.Body)
	}
	var ov struct {
		Roles []struct {
			ID       string `json:"id"`
			Everyone bool   `json:"everyone"`
		} `json:"roles"`
		Channels []struct {
			Channel string `json:"channel"`
			Private bool   `json:"private"`
			Roles   []struct {
				Name string `json:"name"`
			} `json:"roles"`
		} `json:"channels"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &ov)
	if len(ov.Channels) != 2 || ov.Channels[1].Channel != "#staff" || !ov.Channels[1].Private || ov.Channels[1].Roles[0].Name != "Moderator" {
		t.Fatalf("overview %s", w.Body)
	}
	if last := ov.Roles[len(ov.Roles)-1]; last.ID != "everyone" || !last.Everyone {
		t.Fatalf("roles %s", w.Body)
	}

	// auth without role channels: 501 unsupported, which the UI explains
	h.fake.NoRoleChannels = true
	if w := h.do("GET", "/api/roles/everyone/channels", nil, auth...); w.Code != 501 {
		t.Fatalf("old auth: %d %s", w.Code, w.Body)
	}
}
