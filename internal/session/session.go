// Package session is the panel's login state. It lives entirely in an
// encrypted, authenticated HttpOnly cookie (AES-256-GCM with SESSION_KEY):
// the browser holds an opaque blob, the BFF holds the key, and the auth
// service's tokens never reach page scripts.
package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"
)

// Data is what the cookie carries.
type Data struct {
	SessionID    string    `json:"sid"`
	UserID       string    `json:"uid"`
	Username     string    `json:"usr"`
	Role         string    `json:"role"`
	AccessToken  string    `json:"at"`
	AccessExp    time.Time `json:"ate"`
	RefreshToken string    `json:"rt"`
	RefreshExp   time.Time `json:"rte"`
	// CSRF is echoed by the UI in X-CSRF-Token on state-changing requests.
	CSRF     string    `json:"csrf"`
	IssuedAt time.Time `json:"iat"`
	// RBAC: the auth service had roles at sign-in (>= 1.5.0), so access is
	// decided by permissions rather than role=admin.
	RBAC bool `json:"rbac,omitempty"`
}

// Session is a request's mutable view of Data. Services replace the tokens
// after a refresh; the HTTP layer then re-issues the cookie.
type Session struct {
	mu    sync.Mutex
	data  Data
	dirty bool
}

func New(d Data) *Session { return &Session{data: d} }

// Snapshot returns a copy of the current data.
func (s *Session) Snapshot() Data {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data
}

// Update applies fn to the data and marks the session for re-issue.
func (s *Session) Update(fn func(*Data)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.data)
	s.dirty = true
}

// Dirty reports whether the cookie must be re-issued.
func (s *Session) Dirty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dirty
}

// NewCSRFToken returns 32 random bytes, base64url.
func NewCSRFToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ErrInvalid means the cookie is missing, tampered with, from another key, or expired.
var ErrInvalid = errors.New("session: invalid cookie")

const version byte = 1

// Codec seals Data into a cookie and opens it again.
type Codec struct {
	aead   cipher.AEAD
	name   string
	secure bool
	maxAge time.Duration
	now    func() time.Time
}

// NewCodec builds a codec. With secure set the cookie is named
// "__Host-<name>", which browsers only accept over HTTPS, host-only, Path=/.
func NewCodec(key []byte, name string, secure bool, maxAge time.Duration, now func() time.Time) (*Codec, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if secure {
		name = "__Host-" + name
	}
	if now == nil {
		now = time.Now
	}
	return &Codec{aead: aead, name: name, secure: secure, maxAge: maxAge, now: now}, nil
}

// Name is the cookie name.
func (c *Codec) Name() string { return c.name }

// Expiry is when a login made at issuedAt ends: SESSION_MAX_AGE after it,
// or earlier if the refresh token runs out first.
func (c *Codec) Expiry(d Data) time.Time {
	exp := d.IssuedAt.Add(c.maxAge)
	if !d.RefreshExp.IsZero() && d.RefreshExp.Before(exp) {
		exp = d.RefreshExp
	}
	return exp
}

// Seal encrypts d. The cookie name is authenticated as additional data, so
// a value can't be moved to another cookie.
func (c *Codec) Seal(d Data) (string, error) {
	plain, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, c.aead.NonceSize(), c.aead.NonceSize()+len(plain)+c.aead.Overhead()+1)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := c.aead.Seal(nonce, nonce, plain, c.aad())
	return base64.RawURLEncoding.EncodeToString(append([]byte{version}, out...)), nil
}

// Open decrypts and validates a cookie value.
func (c *Codec) Open(value string) (Data, error) {
	var d Data
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) < 1+c.aead.NonceSize()+c.aead.Overhead() || raw[0] != version {
		return d, ErrInvalid
	}
	raw = raw[1:]
	ns := c.aead.NonceSize()
	plain, err := c.aead.Open(nil, raw[:ns], raw[ns:], c.aad())
	if err != nil {
		return d, ErrInvalid
	}
	if err := json.Unmarshal(plain, &d); err != nil {
		return d, ErrInvalid
	}
	if d.SessionID == "" || d.RefreshToken == "" || d.CSRF == "" || !c.now().Before(c.Expiry(d)) {
		return d, ErrInvalid
	}
	return d, nil
}

func (c *Codec) aad() []byte { return []byte(c.name) }

// Read returns the session from the request's cookie.
func (c *Codec) Read(r *http.Request) (Data, error) {
	ck, err := r.Cookie(c.name)
	if err != nil {
		return Data{}, ErrInvalid
	}
	return c.Open(ck.Value)
}

// Write sets the session cookie.
func (c *Codec) Write(w http.ResponseWriter, d Data) error {
	v, err := c.Seal(d)
	if err != nil {
		return err
	}
	exp := c.Expiry(d)
	http.SetCookie(w, &http.Cookie{
		Name: c.name, Value: v, Path: "/", Expires: exp, MaxAge: max(int(exp.Sub(c.now()).Seconds()), 1),
		HttpOnly: true, Secure: c.secure, SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// Clear deletes the session cookie.
func (c *Codec) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: c.name, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: c.secure, SameSite: http.SameSiteLaxMode,
	})
}
