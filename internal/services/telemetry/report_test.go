package telemetry

import "testing"

func TestBucketBoundaries(t *testing.T) {
	for n, want := range map[int64]string{
		-3: "0", 0: "0", 1: "1", 2: "2-10", 10: "2-10", 11: "11-50", 50: "11-50", 51: "51-100",
		100: "51-100", 101: "101-500", 500: "101-500", 501: "501-1k", 1000: "501-1k",
		1001: "1k-5k", 5000: "1k-5k", 5001: "5k+", 1_000_000: "5k+",
	} {
		if got := Bucket(n); got != want {
			t.Errorf("Bucket(%d) = %q, want %q", n, got, want)
		}
	}
	if bucketCap != 5001 || Bucket(bucketCap) != "5k+" || Bucket(bucketCap-1) != "1k-5k" {
		t.Fatal("bucketCap must be the first count of 5k+")
	}
}

func TestNormaliseVersion(t *testing.T) {
	for in, want := range map[string]string{
		"1.48.0":          "1.48.0",
		"v1.9.2":          "1.9.2",
		"17.6-alpine":     "17.6.0",
		"17-alpine":       "unknown",
		"3.7":             "3.7.0",
		"1.2.3-alpine3.2": "1.2.3",
		"29.9.3-cli":      "29.9.3",
		"1.2.3-rc.1":      "1.2.3-rc.1",
		"2.0.0-beta2":     "2.0.0-beta2",
		"1.2.3+build.7":   "1.2.3",
		"latest":          "unknown",
		"edge":            "unknown",
		"":                "unknown",
		"1.2.3.4":         "unknown",
		"sha256:abcdef":   "unknown",
		"12345.1.1":       "unknown",
		"1.2.3-rc.0123456789012345678901234567890123": "unknown", // a pre-release over 32 characters
	} {
		if got := NormaliseVersion(in); got != want {
			t.Errorf("NormaliseVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestErrorClass(t *testing.T) {
	for _, c := range []struct{ kind, step, err, want string }{
		{"update", "preflight", "", "preflight"},
		{"update", "backup", "", "backup"},
		{"update", "stage", "", "pull"},
		{"update", "pull", "", "pull"},
		{"update", "configure", "", "migrate"},
		{"update", "recreate", "", "recreate"},
		{"update", "health", "not healthy after 3m", "health"},
		{"update", "verify", "", "checks"},
		{"update", "self", "", "self_update"},
		{"update", "", "context deadline exceeded", "timeout"},
		{"update", "", "boom", "other"},
		{"rollback", "restore", "", "rollback"},
	} {
		if got := errorClass(c.kind, c.step, c.err); got != c.want {
			t.Errorf("errorClass(%q, %q) = %q, want %q", c.kind, c.step, got, c.want)
		}
	}
}

func TestEnumsMapping(t *testing.T) {
	for in, want := range map[string]string{"OIDC": "oidc", "saml": "saml", "github": "oauth2", "oauth2": "oauth2", "ldap": "other"} {
		if got := ssoKind(in); got != want {
			t.Errorf("ssoKind(%q) = %q", in, got)
		}
	}
	for in, want := range map[string]string{
		"ergo-2.19.1": "ergo", "UnrealIRCd-6.1.8": "unrealircd", "InspIRCd-4": "inspircd", "ngircd-27": "ngircd",
		"solanum-1.0": "solanum", "charybdis-4.1": "charybdis", "ircd-hybrid-8.2": "hybrid", "bahamut-2": "other", "": "unknown",
	} {
		if got := ircdFrom(in); got != want {
			t.Errorf("ircdFrom(%q) = %q", in, got)
		}
	}
	if archOf("amd64") != "amd64" || archOf("arm64") != "arm64" || archOf("riscv64") != "other" {
		t.Fatal("arch")
	}
	for svc, p := range serviceProfile {
		if !contains(ServiceNames, svc) || !contains(ProfileNames, p) {
			t.Errorf("serviceProfile %s → %s isn't in the enums", svc, p)
		}
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestOffValues(t *testing.T) {
	for _, v := range []string{"off", "OFF", "false", "0", "no", " No "} {
		if !Off(v) {
			t.Errorf("Off(%q) = false", v)
		}
	}
	for _, v := range []string{"", "on", "true", "1", "yes"} {
		if Off(v) {
			t.Errorf("Off(%q) = true", v)
		}
	}
	if Endpoint("") != "https://telemetry.boson.chat/v1/report" || Endpoint("http://x:1/") != "http://x:1/v1/report" {
		t.Fatal("Endpoint")
	}
}
