package bosonapi

import (
	"encoding/json"
	"testing"
)

func TestDirectoryShapes(t *testing.T) {
	// /v1 network, wrapped.
	var l Listing
	if err := json.Unmarshal([]byte(`{"network":{"id":"net_1","hostname":"irc.a.org","status":"pending","created_at":"2026-11-01T00:00:00Z","last_checked_at":"2026-11-02T00:00:00Z","origin":"local"},"verification":{"record_name":"_boson.irc.a.org","record_value":"boson-verify=t"}}`), &l); err != nil {
		t.Fatal(err)
	}
	if l.Network.VerificationStatus != "pending" || l.Network.RegisteredAt == nil || l.Network.VerificationLastCheckedAt == nil || l.Verification.RecordValue != "boson-verify=t" || l.Network.Origin != "local" {
		t.Fatalf("wrapped: %+v", l)
	}
	// Bare network with its token.
	l = Listing{}
	if err := json.Unmarshal([]byte(`{"id":"net_1","hostname":"irc.a.org","status":"pending","verification_token":"xyz"}`), &l); err != nil {
		t.Fatal(err)
	}
	if l.Network.ID != "net_1" || l.Verification == nil || l.Verification.RecordName != "_boson.irc.a.org" || l.Verification.RecordValue != "boson-verify=xyz" {
		t.Fatalf("bare: %+v %+v", l, l.Verification)
	}
	// Legacy /servers row keeps its names; the panel's own JSON uses them.
	var s Server
	if err := json.Unmarshal([]byte(`{"id":"s","verification_status":"verified","registered_at":"2026-01-01T00:00:00Z"}`), &s); err != nil || s.VerificationStatus != "verified" || s.RegisteredAt == nil {
		t.Fatalf("legacy: %v %+v", err, s)
	}
	b, _ := json.Marshal(s)
	if string(b) == "" || !json.Valid(b) {
		t.Fatal("marshal")
	}
	var back map[string]any
	_ = json.Unmarshal(b, &back)
	if back["verification_status"] != "verified" {
		t.Fatalf("panel shape: %s", b)
	}
	// Verify result: the backend called it "server".
	var v VerifyResult
	if err := json.Unmarshal([]byte(`{"success":true,"server":{"id":"s","status":"verified"},"report":{"success":true,"results":{}}}`), &v); err != nil || v.Network == nil || v.Network.VerificationStatus != "verified" {
		t.Fatalf("verify: %v %+v", err, v)
	}
}
