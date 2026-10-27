// Package pwa sends Web Push notifications to browsers and phones that
// enabled them (the "Browser push" alert channel). Payloads are encrypted for
// each device (RFC 8291) and signed with the hub's VAPID key (RFC 8292).
package pwa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/SherClockHolmes/webpush-go"

	"hook/internal/store"
)

type Pusher struct {
	st      *store.Store
	pub     string
	priv    string
	subject string // VAPID contact: an https URL or an email address
	log     *slog.Logger
}

// New loads the hub's VAPID key pair, creating it on first run. The private
// key is stored sealed; changing it would invalidate every subscription.
func New(st *store.Store, subject string, log *slog.Logger) (*Pusher, error) {
	p := &Pusher{st: st, subject: subject, log: log}
	pub, err1 := st.Setting("vapid_public")
	priv, err2 := st.SecretSetting("vapid_private")
	if err1 == nil && err2 == nil {
		p.pub, p.priv = string(pub), string(priv)
		return p, nil
	}
	if !errors.Is(err1, store.ErrNotFound) && err1 != nil {
		return nil, err1
	}
	privKey, pubKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return nil, err
	}
	if err := st.SetSecretSetting("vapid_private", []byte(privKey)); err != nil {
		return nil, err
	}
	if err := st.SetSetting("vapid_public", []byte(pubKey)); err != nil {
		return nil, err
	}
	p.pub, p.priv = pubKey, privKey
	return p, nil
}

func (p *Pusher) PublicKey() string { return p.pub }

// Notification is what the service worker shows.
type Notification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url,omitempty"` // opened on tap
	Tag   string `json:"tag,omitempty"` // same tag replaces an older notification
}

// Send delivers to every subscribed device. Devices the push service reports
// as gone (404/410) are removed; it fails only if no device got it.
func (p *Pusher) Send(ctx context.Context, n Notification) (sent int, err error) {
	subs, err := p.st.PushSubscriptions()
	if err != nil {
		return 0, err
	}
	if len(subs) == 0 {
		return 0, errors.New("no devices have turned on notifications yet (Settings → Push notifications)")
	}
	payload, _ := json.Marshal(n)
	var errs []string
	for _, s := range subs {
		resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
			Endpoint: s.Endpoint,
			Keys:     webpush.Keys{P256dh: s.P256dh, Auth: s.Auth},
		}, &webpush.Options{
			Subscriber:      p.subject,
			VAPIDPublicKey:  p.pub,
			VAPIDPrivateKey: p.priv,
			TTL:             6 * 3600, // an alert older than 6 hours isn't worth waking a phone for
			Urgency:         webpush.UrgencyHigh,
			Topic:           topic(n.Tag),
		})
		if err != nil {
			p.st.PushDelivered(s.ID, false)
			errs = append(errs, s.Label+": "+err.Error())
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
			p.log.Info("removing expired push subscription", "device", s.Label)
			p.st.DeletePushEndpoint(s.Endpoint)
		case resp.StatusCode >= 300:
			p.st.PushDelivered(s.ID, false)
			errs = append(errs, fmt.Sprintf("%s: HTTP %d %s", s.Label, resp.StatusCode, strings.TrimSpace(string(body))))
		default:
			p.st.PushDelivered(s.ID, true)
			sent++
		}
	}
	if sent == 0 && len(errs) > 0 {
		return 0, errors.New(strings.Join(errs, "; "))
	}
	return sent, nil
}

// topic lets the push service collapse undelivered messages for the same
// monitor (it allows at most 32 URL-safe characters).
func topic(tag string) string {
	var b strings.Builder
	for _, r := range tag {
		if b.Len() >= 32 {
			break
		}
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
