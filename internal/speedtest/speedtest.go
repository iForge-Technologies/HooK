// Package speedtest measures a server's internet bandwidth and latency
// against Cloudflare's public speed test endpoints, the same ones
// speed.cloudflare.com uses. The endpoint is fixed in the agent; the hub can
// only ask for a test, never choose where the traffic goes.
package speedtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"hook/internal/proto"
)

type Options struct {
	Base     string        // endpoint base URL
	Duration time.Duration // per direction
	MaxDown  int64         // byte budget per direction, so metered servers aren't surprised
	MaxUp    int64
	Streams  int
	Client   *http.Client
}

func Default() Options {
	return Options{Base: "https://speed.cloudflare.com", Duration: 8 * time.Second, MaxDown: 250 << 20, MaxUp: 100 << 20, Streams: 4}
}

const chunk = 25 << 20

// Run measures latency, then download, then upload.
func Run(ctx context.Context, o Options) proto.SpeedResult {
	if o.Client == nil {
		o.Client = &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConnsPerHost: o.Streams, ForceAttemptHTTP2: false}}
	}
	var r proto.SpeedResult
	lat, jit, server, err := latency(ctx, o)
	if err != nil {
		r.Error = "can't reach the speed test server: " + err.Error()
		return r
	}
	r.LatencyMs, r.JitterMs, r.Server = lat, jit, server

	down, n, err := transfer(ctx, o, false)
	r.Bytes += n
	if err != nil {
		r.Error = "download test failed: " + err.Error()
		return r
	}
	up, n, err := transfer(ctx, o, true)
	r.Bytes += n
	if err != nil {
		r.Error = "upload test failed: " + err.Error()
	}
	r.DownBps, r.UpBps = math.Round(down), math.Round(up)
	return r
}

// latency times small requests over a warm connection, from the request
// being written to the first response byte, minus the time the server says it
// spent (Server-Timing). Returns the median and jitter (mean difference
// between consecutive samples), in ms.
func latency(ctx context.Context, o Options) (med, jitter float64, server string, err error) {
	var samples []float64
	for i := range 11 {
		var sent, first time.Time
		trace := &httptrace.ClientTrace{
			WroteRequest:         func(httptrace.WroteRequestInfo) { sent = time.Now() },
			GotFirstResponseByte: func() { first = time.Now() },
		}
		resp, err := get(httptrace.WithClientTrace(ctx, trace), o.Client, o.Base+"/__down?bytes=0")
		if err != nil {
			return 0, 0, "", err
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if i == 0 { // warms DNS, TCP and TLS
			server = serverName(resp, o.Base)
			continue
		}
		if sent.IsZero() || first.Before(sent) {
			continue
		}
		ms := float64(first.Sub(sent).Microseconds())/1000 - serverTime(resp.Header)
		samples = append(samples, math.Max(0, ms))
	}
	if len(samples) < 2 {
		return 0, 0, server, errors.New("couldn't time the requests")
	}
	for i := 1; i < len(samples); i++ {
		jitter += math.Abs(samples[i] - samples[i-1])
	}
	jitter /= float64(len(samples) - 1)
	slices.Sort(samples)
	return round1(samples[len(samples)/2]), round1(jitter), server, nil
}

var durRe = regexp.MustCompile(`(?i);\s*dur=([0-9.]+)`)

// serverTime sums the durations in Server-Timing headers, in ms
// ("cfSpeedEdge;dur=10, cfSpeedWorker;dur=53" → 63).
func serverTime(h http.Header) float64 {
	var total float64
	for _, v := range h.Values("Server-Timing") {
		for _, m := range durRe.FindAllStringSubmatch(v, -1) {
			if d, err := strconv.ParseFloat(m[1], 64); err == nil {
				total += d
			}
		}
	}
	return total
}

// serverName reads the Cloudflare data centre from the cf-ray header
// ("8f1a…-FRA" → "Cloudflare FRA").
func serverName(resp *http.Response, base string) string {
	if ray := resp.Header.Get("Cf-Ray"); ray != "" {
		if i := strings.LastIndexByte(ray, '-'); i >= 0 {
			return "Cloudflare " + ray[i+1:]
		}
		return "Cloudflare"
	}
	if u, err := url.Parse(base); err == nil {
		return u.Host
	}
	return base
}

// transfer runs parallel streams until the time or byte budget runs out and
// returns bits per second and the bytes moved.
func transfer(ctx context.Context, o Options, upload bool) (bps float64, total int64, err error) {
	budget := o.MaxDown
	if upload {
		budget = o.MaxUp
	}
	ctx, cancel := context.WithTimeout(ctx, o.Duration)
	defer cancel()
	var reserved, moved atomic.Int64
	var firstErr error
	var errOnce sync.Once
	take := func() int64 {
		for {
			cur := reserved.Load()
			n := min(int64(chunk), budget-cur)
			if n <= 0 {
				return 0
			}
			if reserved.CompareAndSwap(cur, cur+n) {
				return n
			}
		}
	}
	start := time.Now()
	var wg sync.WaitGroup
	for range max(1, o.Streams) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				n := take()
				if n == 0 {
					return
				}
				var err error
				if upload {
					err = post(ctx, o.Client, o.Base+"/__up", n, &moved)
				} else {
					err = fetch(ctx, o.Client, fmt.Sprintf("%s/__down?bytes=%d", o.Base, n), &moved)
				}
				if err != nil {
					if ctx.Err() == nil {
						errOnce.Do(func() { firstErr = err })
					}
					return
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start).Seconds()
	total = moved.Load()
	if total == 0 {
		if firstErr == nil {
			firstErr = errors.New("no data transferred")
		}
		return 0, 0, firstErr
	}
	return float64(total) * 8 / elapsed, total, nil
}

func get(ctx context.Context, c *http.Client, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "hook-agent speedtest")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func fetch(ctx context.Context, c *http.Client, u string, moved *atomic.Int64) error {
	resp, err := get(ctx, c, u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(counter{moved}, resp.Body)
	return err
}

func post(ctx context.Context, c *http.Client, u string, n int64, moved *atomic.Int64) error {
	body := &zeros{left: n, moved: moved}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, body)
	if err != nil {
		return err
	}
	req.ContentLength = n
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("User-Agent", "hook-agent speedtest")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

type counter struct{ n *atomic.Int64 }

func (c counter) Write(p []byte) (int, error) {
	c.n.Add(int64(len(p)))
	return len(p), nil
}

// zeros is an upload body that counts bytes as the transport reads them.
type zeros struct {
	left  int64
	moved *atomic.Int64
}

func (z *zeros) Read(p []byte) (int, error) {
	if z.left <= 0 {
		return 0, io.EOF
	}
	n := int(min(int64(len(p)), z.left))
	clear(p[:n])
	z.left -= int64(n)
	z.moved.Add(int64(n))
	return n, nil
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
