package session

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func sample(now time.Time) Data {
	return Data{SessionID: "ses_1", UserID: "usr_1", Username: "root", Role: "admin", AccessToken: "at",
		AccessExp: now.Add(15 * time.Minute), RefreshToken: "rt", RefreshExp: now.Add(720 * time.Hour),
		CSRF: NewCSRFToken(), IssuedAt: now}
}

func TestRoundTrip(t *testing.T) {
	now := time.Now()
	c, _ := NewCodec(key(1), "boson_admin", true, 12*time.Hour, func() time.Time { return now })
	if c.Name() != "__Host-boson_admin" {
		t.Fatalf("secure cookie name = %q", c.Name())
	}
	v, err := c.Seal(sample(now))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(v, "rt") && strings.Contains(v, "root") {
		t.Fatal("cookie looks like plaintext")
	}
	d, err := c.Open(v)
	if err != nil || d.Username != "root" || d.RefreshToken != "rt" {
		t.Fatalf("open: %v %+v", err, d)
	}
}

func TestRejectsTamperingOtherKeysAndNames(t *testing.T) {
	now := time.Now()
	c1, _ := NewCodec(key(1), "boson_admin", false, time.Hour, nil)
	c2, _ := NewCodec(key(2), "boson_admin", false, time.Hour, nil)
	c3, _ := NewCodec(key(1), "other", false, time.Hour, nil)
	v, _ := c1.Seal(sample(now))
	if _, err := c2.Open(v); err == nil {
		t.Fatal("opened with another key")
	}
	if _, err := c3.Open(v); err == nil {
		t.Fatal("opened under another cookie name")
	}
	b := []byte(v)
	b[len(b)/2] ^= 1
	if _, err := c1.Open(string(b)); err == nil {
		t.Fatal("opened a tampered cookie")
	}
	for _, junk := range []string{"", "x", "AQ", strings.Repeat("A", 80)} {
		if _, err := c1.Open(junk); err == nil {
			t.Fatalf("opened %q", junk)
		}
	}
}

func TestExpiry(t *testing.T) {
	now := time.Now()
	clock := now
	c, _ := NewCodec(key(1), "s", false, 12*time.Hour, func() time.Time { return clock })
	d := sample(now)
	v, _ := c.Seal(d)
	clock = now.Add(11 * time.Hour)
	if _, err := c.Open(v); err != nil {
		t.Fatalf("valid session rejected: %v", err)
	}
	clock = now.Add(13 * time.Hour)
	if _, err := c.Open(v); err == nil {
		t.Fatal("session past SESSION_MAX_AGE accepted")
	}
	// Refresh token running out first also ends it.
	d.RefreshExp = now.Add(time.Hour)
	v, _ = c.Seal(d)
	clock = now.Add(2 * time.Hour)
	if _, err := c.Open(v); err == nil {
		t.Fatal("session past refresh expiry accepted")
	}
}

func TestCookieAttributes(t *testing.T) {
	now := time.Now()
	c, _ := NewCodec(key(1), "boson_admin", true, time.Hour, nil)
	w := httptest.NewRecorder()
	if err := c.Write(w, sample(now)); err != nil {
		t.Fatal(err)
	}
	ck := w.Result().Cookies()[0]
	if !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" || ck.Domain != "" {
		t.Fatalf("cookie attributes: %+v", ck)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(ck)
	if _, err := c.Read(r); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	c.Clear(w)
	if w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("clear does not expire the cookie")
	}
}
