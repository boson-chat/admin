package authapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

func TestRolesWireFormat(t *testing.T) {
	var got []string
	mux := http.NewServeMux()
	record := func(r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.RequestURI()+" "+strings.TrimSpace(string(b)))
	}
	mux.HandleFunc("GET /v1/me", func(w http.ResponseWriter, r *http.Request) {
		// auth 1.5 puts roles and permissions next to the user
		_, _ = w.Write([]byte(`{"user":{"id":"usr_1","username":"ana","role":"user"},
			"roles":[{"id":"rol_mod","name":"Moderator","color":"#3b82f6","position":50}],
			"permissions":["admin.panel","users.view"]}`))
	})
	mux.HandleFunc("PATCH /v1/admin/roles/{id}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_, _ = w.Write([]byte(`{"role":{"id":"rol_dj","name":"DJ","color":null,"position":3,"builtin":null,"permissions":[],"irc":{"whois_line":"","vhost":"","oper":false}}}`))
	})
	mux.HandleFunc("PUT /v1/admin/users/{id}/roles", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_, _ = w.Write([]byte(`{"roles":[]}`))
	})
	mux.HandleFunc("GET /v1/admin/roles/{id}/members", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_, _ = w.Write([]byte(`{"users":[{"id":"usr_1","username":"ana"}],"next_cursor":null}`))
	})
	mux.HandleFunc("DELETE /v1/admin/roles/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"error":"role_builtin","message":"built-in"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewAuthClient(srv.URL, time.Second, nil)
	ctx := context.Background()

	u, err := c.Me(ctx, "t")
	if err != nil || u.Roles == nil || (*u.Roles)[0].Name != "Moderator" || u.Permissions == nil || len(*u.Permissions) != 2 {
		t.Fatalf("me: %v %+v", err, u)
	}
	name := "DJ"
	if _, err := c.UpdateRole(ctx, "t", "rol_dj", RoleInput{Name: &name, ClearColor: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetUserRoles(ctx, "t", "usr_1", nil); err != nil {
		t.Fatal(err)
	}
	p, err := c.RoleMembers(ctx, "t", "rol_dj", "c1", 25)
	if err != nil || p.NextCursor != "" || len(p.Users) != 1 {
		t.Fatalf("members: %v %+v", err, p)
	}
	want := []string{
		`PATCH /v1/admin/roles/rol_dj {"color":null,"name":"DJ"}`,
		`PUT /v1/admin/users/usr_1/roles {"role_ids":[]}`,
		`GET /v1/admin/roles/rol_dj/members?cursor=c1&limit=25 `,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if e, ok := apperr.As(c.DeleteRole(ctx, "t", "rol_owner")); !ok || e.Status != 409 || e.Code != "role_builtin" {
		t.Fatalf("delete builtin: %v", e)
	}
	// Before auth 1.5 there is no roles route at all.
	if _, err := c.ListRoles(ctx, "t"); !apperr.IsUnsupported(err) || !strings.Contains(err.Error(), "auth ≥ v1.5.0") {
		t.Fatalf("missing roles route: %v", err)
	}
}

func TestRoleInputJSON(t *testing.T) {
	var in RoleInput
	if err := json.Unmarshal([]byte(`{"name":"x","color":null}`), &in); err != nil || !in.ClearColor || in.Color != nil {
		t.Fatalf("color null: %v %+v", err, in)
	}
	in = RoleInput{}
	if err := json.Unmarshal([]byte(`{"color":"#aabbcc","hoist":false}`), &in); err != nil || in.ClearColor || *in.Color != "#aabbcc" || in.Hoist == nil {
		t.Fatalf("color set: %v %+v", err, in)
	}
	b, _ := json.Marshal(RoleInput{})
	if string(b) != "{}" {
		t.Fatalf("empty patch: %s", b)
	}
}
