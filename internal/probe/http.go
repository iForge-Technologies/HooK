package probe

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"strings"
	"time"
)

const maxBody = 2 << 20 // read at most 2 MiB of a response

func checkHTTP(ctx context.Context, s Spec) Result {
	c := s
	method := strings.ToUpper(c.Method)
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if c.Body != "" && method != http.MethodGet && method != http.MethodHead {
		body = strings.NewReader(c.Body)
	}

	// Phase timestamps. GetConn resets them so redirects report the final hop.
	var dnsStart, dnsDone, connStart, connDone, tlsStart, tlsDone, wrote, firstByte time.Time
	trace := &httptrace.ClientTrace{
		GetConn: func(string) {
			dnsStart, dnsDone, connStart, connDone, tlsStart, tlsDone, wrote, firstByte = time.Time{}, time.Time{}, time.Time{}, time.Time{}, time.Time{}, time.Time{}, time.Time{}, time.Time{}
		},
		DNSStart: func(httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone:  func(httptrace.DNSDoneInfo) { dnsDone = time.Now() },
		ConnectStart: func(string, string) {
			if connStart.IsZero() { // happy eyeballs may dial twice
				connStart = time.Now()
			}
		},
		ConnectDone: func(_, _ string, err error) {
			if err == nil {
				connDone = time.Now()
			}
		},
		TLSHandshakeStart:    func() { tlsStart = time.Now() },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { tlsDone = time.Now() },
		WroteRequest:         func(httptrace.WroteRequestInfo) { wrote = time.Now() },
		GotFirstResponseByte: func() { firstByte = time.Now() },
	}

	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), method, s.Target, body)
	if err != nil {
		return Result{Msg: "Bad request: " + err.Error()}
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Cache-Control", "no-cache")
	for k, v := range c.Headers {
		if strings.EqualFold(k, "Host") {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}

	// The hub may check private targets the admin chose, but when the target
	// is public, neither its redirects nor later DNS answers may take the hub
	// onto its own network. (Not through a proxy, which resolves names itself.)
	proxy := http.ProxyFromEnvironment
	pinned := false
	if !s.publicOnly {
		if p, _ := proxy(req); p == nil && targetIsPublic(ctx, req.URL.Hostname()) {
			s.publicOnly, pinned = true, true
		}
	} else {
		proxy = nil // an agent's proxy would sidestep the connect-time guard
	}
	transport := &http.Transport{
		Proxy:             proxy,
		DialContext:       s.dialer().DialContext,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: c.IgnoreTLS},
		DisableKeepAlives: true, // every check measures a fresh connection
		ForceAttemptHTTP2: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if !c.FollowRedirects {
				return http.ErrUseLastResponse
			}
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return nil
		},
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		if pinned && errors.Is(err, errPrivate) {
			return Result{Latency: time.Since(start), Msg: "Blocked: redirected to a private address"}
		}
		return Result{Latency: time.Since(start), Msg: ErrMsg(err)}
	}
	defer resp.Body.Close()
	var page []byte
	if s.Type == "keyword" {
		page, err = io.ReadAll(io.LimitReader(resp.Body, maxBody))
	} else {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	}
	end := time.Now()

	r := Result{Latency: end.Sub(start), Code: resp.StatusCode}
	sent := wrote
	if sent.IsZero() {
		sent = start
	}
	r.Timings = &Timings{
		DNS:      ms(dnsStart, dnsDone),
		Connect:  ms(connStart, connDone),
		TLS:      ms(tlsStart, tlsDone),
		TTFB:     ms(sent, firstByte),
		Transfer: ms(firstByte, end),
		Total:    int(r.Latency.Milliseconds()),
	}
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		leaf := resp.TLS.PeerCertificates[0]
		r.CertExpires, r.CertIssuer = leaf.NotAfter, certIssuer(leaf)
	}
	if err != nil {
		r.Msg = "Reading body: " + ErrMsg(err)
		return r
	}

	if !statusOK(resp.StatusCode, c.ExpectedStatus) {
		r.Msg = fmt.Sprintf("HTTP %d %s (expected %s)", resp.StatusCode, http.StatusText(resp.StatusCode), expected(c.ExpectedStatus))
		return r
	}
	if s.Type == "keyword" && c.Keyword != "" {
		found := bytes.Contains(page, []byte(c.Keyword))
		if found == c.KeywordInvert {
			if found {
				r.Msg = fmt.Sprintf("Keyword %q found (should be absent)", c.Keyword)
			} else {
				r.Msg = fmt.Sprintf("Keyword %q not found", c.Keyword)
			}
			return r
		}
	}
	r.OK = true
	r.Msg = fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	return r
}

func expected(spec string) string {
	if strings.TrimSpace(spec) == "" {
		return "200-299"
	}
	return spec
}

// statusOK matches a code against a spec like "200-299,301" or "2xx,3xx".
func statusOK(code int, spec string) bool {
	for _, part := range strings.Split(expected(spec), ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if len(part) == 3 && strings.HasSuffix(part, "xx") {
			if d, err := strconv.Atoi(part[:1]); err == nil && code/100 == d {
				return true
			}
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			continue
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil {
				continue
			}
		}
		if code >= a && code <= b {
			return true
		}
	}
	return false
}

// ValidStatusSpec reports whether every part of the spec parses.
func ValidStatusSpec(spec string) bool {
	if strings.TrimSpace(spec) == "" {
		return true
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if len(part) == 3 && strings.HasSuffix(part, "xx") && part[0] >= '1' && part[0] <= '5' {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		if _, err := strconv.Atoi(strings.TrimSpace(lo)); err != nil {
			return false
		}
		if isRange {
			if _, err := strconv.Atoi(strings.TrimSpace(hi)); err != nil {
				return false
			}
		}
	}
	return true
}

func checkTLS(ctx context.Context, s Spec) Result {
	addr := s.Target
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "443")
	}
	host, _, _ := net.SplitHostPort(addr)
	d := &tls.Dialer{NetDialer: s.dialer(), Config: &tls.Config{ServerName: host, InsecureSkipVerify: s.IgnoreTLS}}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", addr)
	lat := time.Since(start)
	if err != nil {
		return Result{Latency: lat, Msg: ErrMsg(err)}
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return Result{Latency: lat, Msg: "No certificate presented"}
	}
	leaf := certs[0]
	days := int(time.Until(leaf.NotAfter).Hours() / 24)
	r := Result{OK: true, Latency: lat, CertExpires: leaf.NotAfter, CertIssuer: certIssuer(leaf)}
	if days < 0 {
		r.OK = false
		r.Msg = "Certificate expired " + leaf.NotAfter.Format("2006-01-02")
		return r
	}
	r.Msg = fmt.Sprintf("Valid until %s (%d days) · %s", leaf.NotAfter.Format("2006-01-02"), days, leafName(leaf))
	return r
}

func leafName(c *x509.Certificate) string {
	if c.Subject.CommonName != "" {
		return c.Subject.CommonName
	}
	if len(c.DNSNames) > 0 {
		return c.DNSNames[0]
	}
	return certIssuer(c)
}
