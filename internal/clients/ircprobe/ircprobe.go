// Package ircprobe checks that an IRC server answers: it connects (TLS or
// plaintext), asks for the server's capabilities (CAP LS 302) and, if that
// works, registers briefly to read the server version from RPL_MYINFO (004).
// A server that requires a login (no guests) is up, without a version.
package ircprobe

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Result is one probe.
type Result struct {
	OK bool
	// Latency is the time from dialling to the CAP LS reply.
	Latency time.Duration
	// Version is the server's version from RPL_MYINFO (e.g. "ergo-2.19.1");
	// empty when registration didn't finish within the timeout.
	Version string
	// Detail is the error when the probe failed, or a note when it passed
	// without a version.
	Detail string
	// Users is the number of users on the network from LUSERS (266, else
	// 251), the probe itself not counted; Channels the channels formed
	// (254). Only from Counts; nil when the server didn't say.
	Users    *int
	Channels *int
}

type IRCProberImpl interface {
	// Probe connects to address. With useTLS the handshake sends serverName
	// as SNI; the certificate is not verified here (TLS_CHECK_HOSTS reports
	// on certificates), so an expired cert doesn't read as "IRC down".
	Probe(ctx context.Context, address, serverName string, useTLS bool) Result
}

// CounterImpl also reads the LUSERS numbers the server sends after
// registration (the usage & health stats' IRC users).
type CounterImpl interface {
	Counts(ctx context.Context, address, serverName string, useTLS bool) Result
}

type IRCProber struct {
	Timeout time.Duration
}

func NewIRCProber(timeout time.Duration) IRCProberImpl {
	return &IRCProber{Timeout: timeout}
}

// maxLine bounds a server line (IRCv3 tags + 512 bytes is well under this).
const maxLine = 8 << 10

func (p *IRCProber) Probe(ctx context.Context, address, serverName string, useTLS bool) Result {
	return p.probe(ctx, address, serverName, useTLS, false)
}

// Counts is Probe that stays registered until the end of the MOTD to read
// LUSERS (251 users, 254 channels, 266 global users).
func (p *IRCProber) Counts(ctx context.Context, address, serverName string, useTLS bool) Result {
	return p.probe(ctx, address, serverName, useTLS, true)
}

func (p *IRCProber) probe(ctx context.Context, address, serverName string, useTLS, counts bool) Result {
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	start := time.Now()

	var conn net.Conn
	var err error
	d := &net.Dialer{}
	if useTLS {
		td := tls.Dialer{NetDialer: d, Config: &tls.Config{
			ServerName:         serverName,
			InsecureSkipVerify: true, //nolint:gosec // liveness only; certificates are checked by TLS_CHECK_HOSTS
			MinVersion:         tls.VersionTLS12,
		}}
		conn, err = td.DialContext(ctx, "tcp", address)
	} else {
		conn, err = d.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return Result{Detail: simplify(ctx, err)}
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	r := bufio.NewReaderSize(conn, maxLine)
	send := func(lines ...string) error {
		_, err := io.WriteString(conn, strings.Join(lines, "\r\n")+"\r\n")
		return err
	}

	// 1. Liveness: the server answers CAP LS before registration.
	if err := send("CAP LS 302"); err != nil {
		return Result{Detail: simplify(ctx, err)}
	}
	for {
		m, err := readMessage(r)
		if err != nil {
			return Result{Detail: simplify(ctx, err)}
		}
		switch m.command {
		case "PING":
			_ = send("PONG :" + m.trailing())
			continue
		case "ERROR":
			return Result{Detail: "server closed the connection: " + clean(m.trailing())}
		case "CAP":
			// CAP * LS * :caps…   (continuation) / CAP * LS :caps… (last line)
			if len(m.params) >= 3 && strings.EqualFold(m.params[1], "LS") && m.params[2] == "*" && len(m.params) > 3 {
				continue
			}
		default:
			continue
		}
		break
	}
	res := Result{OK: true, Latency: time.Since(start)}

	// 2. Version: register with a throwaway nick and read RPL_MYINFO.
	// Best effort; a slow or refused registration still counts as up.
	nick := "bosonadm" + randHex(3)
	if err := send("CAP END", "NICK "+nick, "USER boson-admin 0 * :boson admin health check"); err != nil {
		res.Detail = "connected; " + simplify(ctx, err)
		return res
	}
	for {
		m, err := readMessage(r)
		if err != nil {
			res.Detail = "connected; no version (" + simplify(ctx, err) + ")"
			return res
		}
		switch m.command {
		case "PING":
			_ = send("PONG :" + m.trailing())
		case "004": // RPL_MYINFO <nick> <servername> <version> <umodes> <cmodes>
			if len(m.params) >= 3 {
				res.Version = clean(m.params[2])
			}
			if !counts {
				quit(conn, r, send)
				return res
			}
		case "251": // RPL_LUSERCLIENT :There are <u> users and <i> invisible on <s> servers
			if counts && res.Users == nil {
				if n, ok := luserClient(m.trailing()); ok {
					res.Users = &n
				}
			}
		case "254": // RPL_LUSERCHANNELS <nick> <channels> :channels formed
			if counts && len(m.params) >= 2 {
				if n, ok := atoi(m.params[1]); ok {
					res.Channels = &n
				}
			}
		case "266": // RPL_GLOBALUSERS <nick> [<current> <max>] :Current global users …
			if counts {
				if n, ok := globalUsers(m.params); ok {
					res.Users = &n
				}
			}
		case "376", "422": // end of MOTD / no MOTD: LUSERS came before
			if counts {
				if res.Users != nil {
					n := max(*res.Users-1, 0) // the probe itself
					res.Users = &n
				}
				quit(conn, r, send)
				return res
			}
		case "FAIL":
			// Ergo with guests off (accounts.require-sasl): the probe has no
			// account, so it can't register. The server is up all the same.
			if len(m.params) >= 2 && m.params[1] == "ACCOUNT_REQUIRED" {
				res.Detail = "connected; no version (guests are off, the server requires a login)"
				return res
			}
		case "ERROR", "432", "433", "465":
			res.Detail = "connected; registration refused (" + m.command + ")"
			return res
		}
	}
}

// quit says goodbye and reads (for at most a second) up to the server's
// ERROR, draining the welcome burst so the server sees a clean quit instead
// of a reset connection.
func quit(conn net.Conn, r *bufio.Reader, send func(...string) error) {
	if send("QUIT :health check") != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	for {
		m, err := readMessage(r)
		if err != nil || m.command == "ERROR" {
			return
		}
	}
}

type message struct {
	command string
	params  []string
}

func (m message) trailing() string {
	if len(m.params) == 0 {
		return ""
	}
	return m.params[len(m.params)-1]
}

// readMessage reads and parses one IRC line, skipping IRCv3 tags and the prefix.
func readMessage(r *bufio.Reader) (message, error) {
	line, err := r.ReadSlice('\n')
	if err != nil {
		if errors.Is(err, bufio.ErrBufferFull) {
			return message{}, errors.New("line too long")
		}
		return message{}, err
	}
	return parse(strings.TrimRight(string(line), "\r\n")), nil
}

func parse(s string) message {
	if strings.HasPrefix(s, "@") {
		if _, rest, ok := strings.Cut(s, " "); ok {
			s = rest
		} else {
			return message{}
		}
	}
	s = strings.TrimLeft(s, " ")
	if strings.HasPrefix(s, ":") {
		if _, rest, ok := strings.Cut(s, " "); ok {
			s = rest
		} else {
			return message{}
		}
	}
	var m message
	head, trailing, hasTrailing := strings.Cut(s, " :")
	f := strings.Fields(head)
	if len(f) == 0 {
		return m
	}
	m.command = strings.ToUpper(f[0])
	m.params = f[1:]
	if hasTrailing {
		m.params = append(m.params, trailing)
	}
	return m
}

func atoi(s string) (int, bool) {
	n := 0
	if s == "" || len(s) > 9 {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// luserClient reads "There are 5 users and 3 invisible on 1 servers".
func luserClient(s string) (int, bool) {
	f := strings.Fields(s)
	total, found := 0, false
	for i := 1; i < len(f); i++ {
		if w := strings.ToLower(f[i]); w == "users" || w == "invisible" {
			if n, ok := atoi(f[i-1]); ok {
				total += n
				found = true
			}
		}
	}
	return total, found
}

// globalUsers reads 266: "<nick> 7 9 :Current global users 7, max 9" or
// "<nick> :Current global users: 7  Max: 9".
func globalUsers(params []string) (int, bool) {
	if len(params) >= 3 {
		if n, ok := atoi(params[1]); ok {
			return n, true
		}
	}
	if len(params) == 0 {
		return 0, false
	}
	t := params[len(params)-1]
	i := strings.Index(strings.ToLower(t), "users")
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(strings.NewReplacer(":", " ", ",", " ").Replace(t[i+len("users"):]))
	if len(f) == 0 {
		return 0, false
	}
	return atoi(f[0])
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

func simplify(ctx context.Context, err error) string {
	var ne net.Error
	if (errors.As(err, &ne) && ne.Timeout()) || ctx.Err() != nil {
		return "timed out"
	}
	if errors.Is(err, io.EOF) {
		return "connection closed by the server"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "DNS lookup failed: " + dnsErr.Err
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Err != nil {
		return clean(opErr.Err.Error())
	}
	return clean(fmt.Sprint(err))
}
