package ircprobe

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIRC accepts one connection at a time and runs handle on it.
func fakeIRC(t *testing.T, useTLS bool, handle func(r *bufio.Reader, w net.Conn)) (addr string, sni func() string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var gotSNI string
	if useTLS {
		cfg := &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}, GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			mu.Lock()
			gotSNI = h.ServerName
			mu.Unlock()
			return nil, nil
		}}
		ln = tls.NewListener(ln, cfg)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				handle(bufio.NewReader(c), c)
			}()
		}
	}()
	return ln.Addr().String(), func() string { mu.Lock(); defer mu.Unlock(); return gotSNI }
}

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "irc.test"},
		DNSNames: []string{"irc.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// ergoLike answers like Ergo: CAP LS in two lines, then registration.
func ergoLike(r *bufio.Reader, w net.Conn) {
	write := func(s string) { _, _ = w.Write([]byte(s + "\r\n")) }
	write(":irc.test NOTICE * :*** Looking up your hostname...")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "CAP":
			if len(f) > 1 && f[1] == "LS" {
				write("@time=2026-11-13T00:00:00.000Z :irc.test CAP * LS * :account-notify away-notify")
				write(":irc.test CAP * LS :sasl=PLAIN,EXTERNAL server-time")
			}
		case "USER":
			write("PING :cookie")
		case "PONG":
			write(":irc.test 001 x :Welcome")
			write(":irc.test 004 x irc.test ergo-2.19.1 BERTZios CEIMRUabefhiklmnoqstuv Iabefhkloqv")
		case "QUIT":
			write("ERROR :Quit")
			return
		}
	}
}

func TestProbeTLSWithVersion(t *testing.T) {
	addr, sni := fakeIRC(t, true, ergoLike)
	r := NewIRCProber(2*time.Second).Probe(context.Background(), addr, "irc.example.com", true)
	if !r.OK || r.Version != "ergo-2.19.1" || r.Detail != "" || r.Latency <= 0 {
		t.Fatalf("got %+v", r)
	}
	if sni() != "irc.example.com" {
		t.Fatalf("SNI %q", sni())
	}
}

func TestProbePlaintext(t *testing.T) {
	addr, _ := fakeIRC(t, false, ergoLike)
	r := NewIRCProber(2*time.Second).Probe(context.Background(), addr, "", false)
	if !r.OK || r.Version != "ergo-2.19.1" {
		t.Fatalf("got %+v", r)
	}
}

func TestProbeUpWithoutVersion(t *testing.T) {
	// Answers CAP LS, then never finishes registration.
	addr, _ := fakeIRC(t, false, func(r *bufio.Reader, w net.Conn) {
		line, _ := r.ReadString('\n')
		if strings.HasPrefix(line, "CAP LS") {
			_, _ = w.Write([]byte(":irc.test CAP * LS :sasl\r\n"))
		}
		_, _ = r.ReadString(0)
	})
	r := NewIRCProber(300*time.Millisecond).Probe(context.Background(), addr, "", false)
	if !r.OK || r.Version != "" || !strings.Contains(r.Detail, "no version") {
		t.Fatalf("got %+v", r)
	}
}

func TestProbeSilentServer(t *testing.T) {
	addr, _ := fakeIRC(t, false, func(r *bufio.Reader, _ net.Conn) { _, _ = r.ReadString(0) })
	r := NewIRCProber(200*time.Millisecond).Probe(context.Background(), addr, "", false)
	if r.OK || r.Detail != "timed out" {
		t.Fatalf("got %+v", r)
	}
}

func TestProbeServerError(t *testing.T) {
	addr, _ := fakeIRC(t, false, func(_ *bufio.Reader, w net.Conn) {
		_, _ = w.Write([]byte("ERROR :Closing Link: too many connections\r\n"))
	})
	r := NewIRCProber(time.Second).Probe(context.Background(), addr, "", false)
	if r.OK || !strings.Contains(r.Detail, "too many connections") {
		t.Fatalf("got %+v", r)
	}
}

func TestProbeRefused(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	r := NewIRCProber(time.Second).Probe(context.Background(), addr, "", false)
	if r.OK || r.Detail == "" {
		t.Fatalf("got %+v", r)
	}
}

func TestProbeTLSAgainstPlaintext(t *testing.T) {
	addr, _ := fakeIRC(t, false, func(r *bufio.Reader, w net.Conn) {
		_, _ = w.Write([]byte(":irc.test NOTICE * :hello\r\n"))
		_, _ = r.ReadString(0)
	})
	r := NewIRCProber(500*time.Millisecond).Probe(context.Background(), addr, "irc.test", true)
	if r.OK {
		t.Fatalf("got %+v", r)
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		in      string
		command string
		params  []string
	}{
		{"@a=b;c :srv CAP * LS * :x y", "CAP", []string{"*", "LS", "*", "x y"}},
		{":srv 004 nick srv ergo-2.19.1 iw ov", "004", []string{"nick", "srv", "ergo-2.19.1", "iw", "ov"}},
		{"PING :abc", "PING", []string{"abc"}},
		{"ping", "PING", nil},
		{"", "", nil},
	}
	for _, c := range cases {
		m := parse(c.in)
		if m.command != c.command || strings.Join(m.params, "|") != strings.Join(c.params, "|") {
			t.Errorf("%q: %+v", c.in, m)
		}
	}
}
