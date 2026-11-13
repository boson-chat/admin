// Package tlsprobe dials TLS endpoints and reports their certificates.
package tlsprobe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"time"
)

// Cert describes the leaf certificate an endpoint presented.
type Cert struct {
	Subject   string    `json:"subject"`
	Issuer    string    `json:"issuer"`
	DNSNames  []string  `json:"dns_names"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
	// Trusted: the chain verifies against the system roots for ServerName.
	Trusted     bool   `json:"trusted"`
	VerifyError string `json:"verify_error,omitempty"`
}

type TLSProberImpl interface {
	// Probe dials address, sends serverName as SNI and returns the leaf
	// certificate. Untrusted certificates are returned too (Trusted=false).
	Probe(ctx context.Context, serverName, address string) (*Cert, error)
}

type TLSProber struct {
	Dialer net.Dialer
	// Roots overrides the system roots (tests).
	Roots *x509.CertPool
	Now   func() time.Time
}

func NewTLSProber(timeout time.Duration) TLSProberImpl {
	return &TLSProber{Dialer: net.Dialer{Timeout: timeout}, Now: time.Now}
}

func (p *TLSProber) Probe(ctx context.Context, serverName, address string) (*Cert, error) {
	d := tls.Dialer{NetDialer: &p.Dialer, Config: &tls.Config{
		ServerName: serverName,
		// We want to see expired / self-signed certs too; verification is
		// done below and reported, not enforced.
		InsecureSkipVerify: true, //nolint:gosec // inspection only, nothing is sent
		MinVersion:         tls.VersionTLS12,
	}}
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, simplify(err)
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, errors.New("no certificate presented")
	}
	leaf := state.PeerCertificates[0]
	c := &Cert{
		Subject: leaf.Subject.CommonName, Issuer: issuerName(leaf), DNSNames: leaf.DNSNames,
		NotBefore: leaf.NotBefore, NotAfter: leaf.NotAfter,
	}
	inter := x509.NewCertPool()
	for _, ic := range state.PeerCertificates[1:] {
		inter.AddCert(ic)
	}
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	_, verr := leaf.Verify(x509.VerifyOptions{DNSName: serverName, Intermediates: inter, Roots: p.Roots, CurrentTime: now()})
	c.Trusted = verr == nil
	if verr != nil {
		c.VerifyError = verifyReason(verr)
	}
	return c, nil
}

func issuerName(c *x509.Certificate) string {
	if c.Issuer.CommonName != "" {
		if len(c.Issuer.Organization) > 0 && !strings.Contains(c.Issuer.CommonName, c.Issuer.Organization[0]) {
			return c.Issuer.Organization[0] + " " + c.Issuer.CommonName
		}
		return c.Issuer.CommonName
	}
	if len(c.Issuer.Organization) > 0 {
		return c.Issuer.Organization[0]
	}
	return c.Issuer.String()
}

func verifyReason(err error) string {
	var ua x509.UnknownAuthorityError
	var hn x509.HostnameError
	var ci x509.CertificateInvalidError
	switch {
	case errors.As(err, &ua):
		return "unknown authority (self-signed or private CA)"
	case errors.As(err, &hn):
		return "certificate is not valid for this hostname"
	case errors.As(err, &ci):
		if ci.Reason == x509.Expired {
			return "expired or not yet valid"
		}
		return "invalid certificate"
	}
	return err.Error()
}

func simplify(err error) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return errors.New("timed out")
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return errors.New("DNS lookup failed: " + dnsErr.Err)
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Err != nil {
		return opErr.Err
	}
	return err
}
