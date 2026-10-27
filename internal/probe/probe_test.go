package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatusOK(t *testing.T) {
	for _, tc := range []struct {
		code int
		spec string
		want bool
	}{
		{200, "", true}, {204, "", true}, {301, "", false},
		{301, "200-299,301", true}, {404, "2xx,3xx", false}, {302, "2xx, 3xx", true},
		{500, "500", true}, {418, "400-417", false},
	} {
		if got := statusOK(tc.code, tc.spec); got != tc.want {
			t.Errorf("statusOK(%d, %q) = %v", tc.code, tc.spec, got)
		}
	}
	if ValidStatusSpec("200-abc") || ValidStatusSpec("9xx") || !ValidStatusSpec("2xx, 301, 400-410") {
		t.Error("ValidStatusSpec wrong")
	}
}

func TestDNSMatch(t *testing.T) {
	if dnsMatch([]string{"1.2.3.40"}, "1.2.3.4", "A") {
		t.Error("A records must match exactly")
	}
	if !dnsMatch([]string{"Example.COM."}, "example.com", "CNAME") {
		t.Error("case and trailing dot should be ignored")
	}
	if !dnsMatch([]string{"v=spf1 include:_spf.google.com ~all"}, "_spf.google.com", "TXT") {
		t.Error("TXT should match by substring")
	}
	if !dnsMatch([]string{"10 mx.example.com."}, "mx.example.com", "MX") {
		t.Error("MX should match by host")
	}
}

func TestHTTPKeyword(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("<h1>All systems operational</h1>"))
	}))
	defer srv.Close()

	s := Spec{Type: "keyword", Target: srv.URL, Timeout: 5, Keyword: "operational", FollowRedirects: true}
	if r := Run(context.Background(), s); !r.OK || r.Timings == nil || r.Code != 200 {
		t.Fatalf("keyword present: %+v", r)
	}
	s.KeywordInvert = true
	if r := Run(context.Background(), s); r.OK {
		t.Fatal("inverted keyword should fail")
	}
	if r := Run(context.Background(), Spec{Type: "http", Target: srv.URL + "/missing", Timeout: 5}); r.OK || r.Code != 404 {
		t.Fatalf("404 should be down: %+v", r)
	}
	if r := Run(context.Background(), Spec{Type: "tcp", Target: srv.Listener.Addr().String(), Timeout: 5}); !r.OK {
		t.Fatalf("tcp: %+v", r)
	}
	remote := Spec{Type: "http", Target: "https://x", Headers: map[string]string{"Authorization": "Bearer t"}, Body: "b"}.ForRemote()
	if remote.Headers != nil || remote.Body != "" {
		t.Fatal("ForRemote must strip headers and body")
	}
}

func TestPublicOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer srv.Close()
	// The same loopback target works for the hub's own check but is refused
	// when an agent runs it on the hub's behalf.
	if r := Run(context.Background(), Spec{Type: "http", Target: srv.URL, Timeout: 5}); !r.OK {
		t.Fatalf("local check: %+v", r)
	}
	for _, s := range []Spec{
		{Type: "http", Target: srv.URL, Timeout: 5},
		{Type: "tcp", Target: srv.Listener.Addr().String(), Timeout: 5},
		{Type: "tls", Target: "127.0.0.1:443", Timeout: 2},
		{Type: "http", Target: "http://169.254.169.254/latest/meta-data/", Timeout: 2},
		{Type: "dns", Target: "example.com", DNSResolver: "10.0.0.2", Timeout: 2},
	} {
		if r := RunPublicOnly(context.Background(), s); r.OK || !WasRefused(r) {
			t.Errorf("%s %s should be refused, got %+v", s.Type, s.Target, r)
		}
	}
}
