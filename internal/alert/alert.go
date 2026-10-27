// Package alert formats monitor and server events and delivers them to
// notification channels.
package alert

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hook/internal/store"
)

// Event kinds. Monitors raise down/up/cert; servers raise offline/online and
// threshold/recovered.
const (
	KindDown      = "down"
	KindUp        = "up"
	KindCert      = "cert"
	KindTest      = "test"
	KindOffline   = "offline"
	KindOnline    = "online"
	KindThreshold = "threshold"
	KindRecovered = "recovered"
	KindContainer = "container"    // a container stopped, is unhealthy or crash-looping
	KindContOK    = "container_ok" // …and is running fine again
	KindRestart   = "restart"      // a running container restarted
	KindService   = "service"      // a watched systemd unit isn't active
	KindServiceOK = "service_ok"
	KindForecast  = "forecast" // the fullest disk is projected to fill soon
)

type Event struct {
	Kind   string
	Source string // "monitor" | "server"
	ID     int64
	Name   string
	Target string // monitor URL/host, or the server's hostname
	Type   string // monitor type, or "server"
	Msg    string
	At     time.Time

	Downtime time.Duration // up, online, recovered

	CertDays    int // cert
	CertIssuer  string
	CertExpires int64

	Subject string // container or unit name

	Metric  string  // threshold/recovered: cpu | mem | disk
	Value   float64 // current value, %
	Peak    float64
	Limit   float64
	Minutes int
}

// MonitorEvent fills an Event from a monitor.
func MonitorEvent(kind string, m *store.Monitor, at time.Time) Event {
	return Event{Kind: kind, Source: "monitor", ID: m.ID, Name: m.Name, Target: m.Target, Type: m.Type, At: at,
		CertIssuer: m.CertIssuer, CertExpires: m.CertExpires}
}

// ServerEvent fills an Event from a server.
func ServerEvent(kind string, h *store.Host, at time.Time) Event {
	target := h.Info.Hostname
	if h.RemoteIP != "" {
		target = strings.TrimSpace(target + " (" + h.RemoteIP + ")")
	}
	return Event{Kind: kind, Source: "server", ID: h.ID, Name: h.Name, Target: target, Type: "server", At: at}
}

// Message is the channel-neutral rendering of an Event.
type Message struct {
	Event *Event
	Title string
	Body  string
	URL   string
	Color int // embed accent for Discord
}

type Dispatcher struct {
	// Push delivers to browsers and phones (the "browser" channel type).
	Push func(ctx context.Context, title, body, path, tag string) error

	// Muted, when set, silences alerts for monitors and servers that are in
	// a maintenance window. Test messages always go through.
	Muted func(source string, id int64) bool

	store     *store.Store
	publicURL string
	client    *http.Client
	log       *slog.Logger
}

func NewDispatcher(s *store.Store, publicURL string, log *slog.Logger) *Dispatcher {
	return &Dispatcher{store: s, publicURL: publicURL, client: &http.Client{Timeout: 15 * time.Second}, log: log}
}

// Notify sends ev to every active channel of its monitor or server, in the background.
func (d *Dispatcher) Notify(ev Event) {
	if d.Muted != nil && d.Muted(ev.Source, ev.ID) {
		d.log.Info("alert muted by maintenance", "source", ev.Source, "name", ev.Name, "kind", ev.Kind)
		return
	}
	go func() {
		var chans []*store.Channel
		var err error
		if ev.Source == "server" {
			chans, err = d.store.HostChannels(ev.ID)
		} else {
			chans, err = d.store.MonitorChannels(ev.ID)
		}
		if err != nil {
			d.log.Error("load channels", "source", ev.Source, "id", ev.ID, "err", err)
			return
		}
		msg := d.format(&ev)
		for _, ch := range chans {
			go func(ch *store.Channel) {
				for attempt := 1; attempt <= 2; attempt++ {
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					err := d.send(ctx, ch, msg)
					cancel()
					if err == nil {
						return
					}
					d.log.Warn("alert delivery failed", "channel", ch.Name, "type", ch.Type, "attempt", attempt, "err", err)
					time.Sleep(5 * time.Second)
				}
			}(ch)
		}
	}()
}

// Test sends a test message through one channel and waits for the result.
func (d *Dispatcher) Test(ctx context.Context, ch *store.Channel) error {
	ev := &Event{Kind: KindTest, Source: "monitor", Name: "Hook", Target: d.publicURL, At: time.Now()}
	return d.send(ctx, ch, d.format(ev))
}

func (d *Dispatcher) send(ctx context.Context, ch *store.Channel, m Message) error {
	switch ch.Type {
	case "telegram":
		return d.telegram(ctx, ch.Config, m)
	case "discord":
		return d.discord(ctx, ch.Config, m)
	case "slack":
		return d.slack(ctx, ch.Config, m)
	case "email":
		return d.email(ctx, ch.Config, m)
	case "webhook":
		return d.webhook(ctx, ch.Config, m)
	case "ntfy":
		return d.ntfy(ctx, ch.Config, m)
	case "browser":
		if d.Push == nil {
			return fmt.Errorf("browser push isn't available")
		}
		path, tag := "/", "hook"
		if ev := m.Event; ev != nil && ev.ID != 0 {
			kind := "monitors"
			if ev.Source == "server" {
				kind = "servers"
			}
			path = "/" + kind + "/" + strconv.FormatInt(ev.ID, 10)
			tag = ev.Source + "-" + strconv.FormatInt(ev.ID, 10)
		}
		return d.Push(ctx, m.Title, m.Body, path, tag)
	}
	return fmt.Errorf("unknown channel type %q", ch.Type)
}

func (d *Dispatcher) link(ev *Event) string {
	if d.publicURL == "" || ev.ID == 0 {
		return d.publicURL
	}
	path := "/monitors/"
	if ev.Source == "server" {
		path = "/servers/"
	}
	return d.publicURL + path + strconv.FormatInt(ev.ID, 10)
}

var metricName = map[string]string{"cpu": "CPU", "mem": "Memory", "disk": "Disk"}

func (d *Dispatcher) format(ev *Event) Message {
	when := ev.At.Format("02 Jan 2006 15:04:05 MST")
	msg := Message{Event: ev, URL: d.link(ev)}
	switch ev.Kind {
	case KindDown:
		msg.Title = fmt.Sprintf("🔴 %s is DOWN", ev.Name)
		msg.Body = fmt.Sprintf("%s\nReason: %s\n%s", ev.Target, ev.Msg, when)
		msg.Color = 0xFF4D5E
	case KindUp:
		msg.Title = fmt.Sprintf("🟢 %s is back UP", ev.Name)
		msg.Body = fmt.Sprintf("%s\nDown for %s\n%s", ev.Target, HumanDuration(ev.Downtime), when)
		msg.Color = 0x3BF08C
	case KindCert:
		msg.Title = fmt.Sprintf("🔒 %s: certificate expires in %d days", ev.Name, ev.CertDays)
		if ev.CertDays < 0 {
			msg.Title = fmt.Sprintf("🔒 %s: certificate has EXPIRED", ev.Name)
		}
		msg.Body = fmt.Sprintf("%s\nIssuer: %s\nExpires: %s", ev.Target, ev.CertIssuer, time.UnixMilli(ev.CertExpires).Format("02 Jan 2006"))
		msg.Color = 0xFFB800
	case KindOffline:
		msg.Title = fmt.Sprintf("🔴 %s stopped reporting", ev.Name)
		msg.Body = fmt.Sprintf("%s\nNo data from the agent since %s. The server may be down, unreachable, or the agent stopped.", ev.Target, when)
		msg.Color = 0xFF4D5E
	case KindOnline:
		msg.Title = fmt.Sprintf("🟢 %s is reporting again", ev.Name)
		msg.Body = fmt.Sprintf("%s\nSilent for %s\n%s", ev.Target, HumanDuration(ev.Downtime), when)
		msg.Color = 0x3BF08C
	case KindThreshold:
		msg.Title = fmt.Sprintf("🟠 %s: %s at %.0f%%", ev.Name, metricName[ev.Metric], ev.Value)
		why := ""
		if ev.Msg != "" {
			why = " (" + ev.Msg + ")"
		}
		msg.Body = fmt.Sprintf("%s\nAbove %.0f%% for %d min%s\n%s", ev.Target, ev.Limit, ev.Minutes, why, when)
		msg.Color = 0xFFB800
	case KindRecovered:
		msg.Title = fmt.Sprintf("🟢 %s: %s back to %.0f%%", ev.Name, metricName[ev.Metric], ev.Value)
		msg.Body = fmt.Sprintf("%s\nHigh for %s, peak %.0f%%\n%s", ev.Target, HumanDuration(ev.Downtime), ev.Peak, when)
		msg.Color = 0x3BF08C
	case KindContainer:
		msg.Title = fmt.Sprintf("🔴 %s: container %s %s", ev.Name, ev.Subject, ev.Msg)
		msg.Body = fmt.Sprintf("%s\n%s", ev.Target, when)
		msg.Color = 0xFF4D5E
	case KindContOK:
		msg.Title = fmt.Sprintf("🟢 %s: container %s is running again", ev.Name, ev.Subject)
		msg.Body = fmt.Sprintf("%s\nProblem lasted %s\n%s", ev.Target, HumanDuration(ev.Downtime), when)
		msg.Color = 0x3BF08C
	case KindRestart:
		msg.Title = fmt.Sprintf("🟠 %s: container %s restarted", ev.Name, ev.Subject)
		msg.Body = fmt.Sprintf("%s\n%s\n%s", ev.Target, ev.Msg, when)
		msg.Color = 0xFFB800
	case KindService:
		msg.Title = fmt.Sprintf("🔴 %s: %s is %s", ev.Name, ev.Subject, ev.Msg)
		msg.Body = fmt.Sprintf("%s\n%s", ev.Target, when)
		msg.Color = 0xFF4D5E
	case KindForecast:
		msg.Title = fmt.Sprintf("💽 %s: disk full in about %.0f days", ev.Name, ev.Peak)
		msg.Body = ev.Target + " · " + ev.Msg + " · " + when
		msg.Color = 0xFFB800
	case KindServiceOK:
		msg.Title = fmt.Sprintf("🟢 %s: %s is active again", ev.Name, ev.Subject)
		msg.Body = fmt.Sprintf("%s\nWas down for %s\n%s", ev.Target, HumanDuration(ev.Downtime), when)
		msg.Color = 0x3BF08C
	default:
		msg.Title = "✅ Hook test notification"
		msg.Body = "If you can read this, the channel works.\n" + when
		msg.Color = 0xA9B2FF
	}
	return msg
}

func HumanDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	switch {
	case h >= 24:
		return fmt.Sprintf("%dd %dh", h/24, h%24)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
