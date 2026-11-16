package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

type usersResp struct {
	Users []struct {
		ID               string     `json:"id"`
		LastSignInAt     *time.Time `json:"last_sign_in_at"`
		LastSignInMethod *string    `json:"last_sign_in_method"`
		LastSignInIP     *string    `json:"last_sign_in_ip"`
		LastActiveAt     *time.Time `json:"last_active_at"`
		ActivityTracked  bool       `json:"activity_tracked"`
	} `json:"users"`
	Sort              string `json:"sort"`
	ActivitySupported bool   `json:"activity_supported"`
}

// Users list: sort is validated and passed to auth, and the response says
// whether auth reported activity (so whether the sort applied).
func TestListUsersSortAndActivity(t *testing.T) {
	h := newHarness(t)
	c, _ := h.login()

	if w := h.do("GET", "/api/users?sort=bogus", nil, withCookie(c)); w.Code != http.StatusBadRequest {
		t.Fatalf("bogus sort: %d %s", w.Code, w.Body)
	}

	// An auth without activity: the sort is passed on but reported as not applied.
	w := h.do("GET", "/api/users?sort=last_active", nil, withCookie(c))
	var r usersResp
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil || w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if h.fake.LastListSort != "last_active" || r.ActivitySupported || r.Sort != "created" {
		t.Fatalf("old auth: fake sort %q, resp %+v", h.fake.LastListSort, r)
	}

	// An auth with activity.
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	method, ip := "oidc:corp", "203.0.113.9"
	bob := h.fake.Users["usr_bob"]
	bob.ActivityTracked, bob.LastSignInAt, bob.LastSignInMethod, bob.LastSignInIP, bob.LastActiveAt = true, &at, &method, &ip, &at
	h.fake.Users["usr_admin"].ActivityTracked = true // never signed in: nulls
	w = h.do("GET", "/api/users?sort=last_sign_in", nil, withCookie(c))
	r = usersResp{}
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil || w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if !r.ActivitySupported || r.Sort != "last_sign_in" || h.fake.LastListSort != "last_sign_in" {
		t.Fatalf("new auth: %+v", r)
	}
	for _, u := range r.Users {
		switch u.ID {
		case "usr_bob":
			if u.LastActiveAt == nil || !u.LastActiveAt.Equal(at) || *u.LastSignInMethod != method || *u.LastSignInIP != ip || !u.ActivityTracked {
				t.Fatalf("bob: %+v", u)
			}
		case "usr_admin":
			if u.LastActiveAt != nil || u.LastSignInAt != nil || !u.ActivityTracked {
				t.Fatalf("admin (never seen): %+v", u)
			}
		}
	}

	// Detail carries the same fields.
	w = h.do("GET", "/api/users/usr_bob", nil, withCookie(c))
	var d struct {
		User map[string]any `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || w.Code != 200 {
		t.Fatalf("detail: %d %s", w.Code, w.Body)
	}
	if d.User["last_sign_in_method"] != method || d.User["last_sign_in_ip"] != ip || d.User["last_active_at"] == nil || d.User["activity_tracked"] != true {
		t.Fatalf("detail: %s", w.Body)
	}
}
