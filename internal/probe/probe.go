// Package probe runs one network check (HTTP, keyword, TCP, DNS, TLS) from a
// self-contained Spec. It has no storage or database dependencies, so both
// the hub and the agent can use it: the agent runs it for multi-location
// confirmation without pulling in database drivers.
package probe

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

const UserAgent = "Hook-Monitor/0.1 (+uptime check)"

// Types this package can run.
var Types = []string{"http", "keyword", "tcp", "dns", "tls"}

// Spec is everything a network check needs. It deliberately has no secrets
// beyond what the monitor's owner put in Headers; specs sent to remote agents
// never include headers or bodies (see ForRemote).
type Spec struct {
	Type            string            `json:"type"`
	Target          string            `json:"target"`
	Timeout         int               `json:"timeout"` // seconds
	Method          string            `json:"method,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	ExpectedStatus  string            `json:"expectedStatus,omitempty"`
	FollowRedirects bool              `json:"followRedirects,omitempty"`
	IgnoreTLS       bool              `json:"ignoreTls,omitempty"`
	Keyword         string            `json:"keyword,omitempty"`
	KeywordInvert   bool              `json:"keywordInvert,omitempty"`
	DNSRecord       string            `json:"dnsRecord,omitempty"`
	DNSResolver     string            `json:"dnsResolver,omitempty"`
	DNSExpected     string            `json:"dnsExpected,omitempty"`

	publicOnly bool // set by RunPublicOnly; never serialised
}

// Timings is the HTTP request waterfall, in milliseconds.
type Timings struct {
	DNS      int `json:"dns"`
	Connect  int `json:"connect"`
	TLS      int `json:"tls"`
	TTFB     int `json:"ttfb"` // server wait after the request was sent
	Transfer int `json:"transfer"`
	Total    int `json:"total"`
}

type Result struct {
	OK          bool
	Latency     time.Duration
	Code        int
	Msg         string
	Timings     *Timings
	CertExpires time.Time // zero when the check saw no certificate
	CertIssuer  string
	Partial     bool // failed here but succeeded from other locations
}

func Run(ctx context.Context, s Spec) Result {
	timeout := time.Duration(max(1, s.Timeout)) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var r Result
	switch s.Type {
	case "http", "keyword":
		r = checkHTTP(ctx, s)
	case "tcp":
		r = checkTCP(ctx, s)
	case "dns":
		r = checkDNS(ctx, s)
	case "tls":
		r = checkTLS(ctx, s)
	default:
		r = Result{Msg: "unknown check type " + s.Type}
	}
	if !r.OK && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		r.Msg = fmt.Sprintf("Timed out after %ds", max(1, s.Timeout))
	}
	return Clip(r)
}

// Clip keeps messages short enough for heartbeats and alerts.
func Clip(r Result) Result {
	if len(r.Msg) > 300 {
		r.Msg = r.Msg[:300] + "…"
	}
	return r
}

// ErrMsg turns network errors into short, readable reasons.
func ErrMsg(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return "DNS: host not found (" + dnsErr.Name + ")"
		}
		return "DNS: " + dnsErr.Err
	}
	var certErr *x509.CertificateInvalidError
	if errors.As(err, &certErr) {
		return "TLS: " + certErr.Error()
	}
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return "TLS: " + hostErr.Error()
	}
	var authErr x509.UnknownAuthorityError
	if errors.As(err, &authErr) {
		return "TLS: certificate signed by unknown authority"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "actively refused"):
		return "Connection refused"
	case strings.Contains(msg, "connection reset"), strings.Contains(msg, "forcibly closed"):
		return "Connection reset"
	case strings.Contains(msg, "no route to host"), strings.Contains(msg, "unreachable"):
		return "Host unreachable"
	}
	return msg
}

func ms(a, b time.Time) int {
	if a.IsZero() || b.IsZero() || b.Before(a) {
		return 0
	}
	return int(b.Sub(a).Milliseconds())
}

func certIssuer(c *x509.Certificate) string {
	if len(c.Issuer.Organization) > 0 {
		return c.Issuer.Organization[0]
	}
	return c.Issuer.CommonName
}

func checkTCP(ctx context.Context, s Spec) Result {
	start := time.Now()
	conn, err := s.dialer().DialContext(ctx, "tcp", s.Target)
	lat := time.Since(start)
	if err != nil {
		return Result{Latency: lat, Msg: ErrMsg(err)}
	}
	conn.Close()
	return Result{OK: true, Latency: lat, Msg: "Port open"}
}

// ForRemote strips what must not leave the hub: request headers (often auth
// tokens) and bodies. Remote locations check reachability, not auth.
func (s Spec) ForRemote() Spec {
	s.Headers, s.Body = nil, ""
	return s
}
