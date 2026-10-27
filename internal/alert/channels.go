package alert

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

// TypeSpec lists the config fields a channel type needs. Secret fields are
// masked before they reach the browser.
type TypeSpec struct {
	Required []string
	Secret   []string
	URLs     []string // fields that must be http(s) URLs
	// Dest fields decide where the secrets go. A stored secret is only kept
	// (sent back masked) while these stay the same.
	Dest []string
}

var Types = map[string]TypeSpec{
	"telegram": {Required: []string{"botToken", "chatId"}, Secret: []string{"botToken"}},
	"discord":  {Required: []string{"webhookUrl"}, Secret: []string{"webhookUrl"}, URLs: []string{"webhookUrl"}},
	"slack":    {Required: []string{"webhookUrl"}, Secret: []string{"webhookUrl"}, URLs: []string{"webhookUrl"}},
	"email":    {Required: []string{"host", "port", "from", "to"}, Secret: []string{"password"}, Dest: []string{"host", "port", "security", "username"}},
	"webhook":  {Required: []string{"url"}, Secret: []string{"secret"}, URLs: []string{"url"}, Dest: []string{"url"}},
	"ntfy":     {Required: []string{"topic"}, Secret: []string{"token"}, URLs: []string{"server"}, Dest: []string{"server"}},
	"browser":  {}, // Web Push to every device that enabled it in Settings
}

func Validate(kind string, cfg map[string]string) error {
	spec, ok := Types[kind]
	if !ok {
		return fmt.Errorf("unknown channel type %q", kind)
	}
	for _, f := range spec.Required {
		if strings.TrimSpace(cfg[f]) == "" {
			return fmt.Errorf("%s is required", f)
		}
	}
	for _, f := range spec.URLs {
		if v := cfg[f]; v != "" {
			u, err := url.Parse(v)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				return fmt.Errorf("%s must be an http(s) URL", f)
			}
		}
	}
	return nil
}

func (d *Dispatcher) postJSON(ctx context.Context, u string, body any, header http.Header) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header[k] = v
	}
	return d.do(req)
}

// StripURL drops the URL from a transport error, keeping the operation and
// host. Webhook and dead-man URLs carry their secret in the path or query,
// and these errors end up in logs.
func StripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		host := ""
		if u, perr := url.Parse(ue.URL); perr == nil {
			host = u.Host
		}
		return fmt.Errorf("%s %s: %w", ue.Op, host, ue.Err)
	}
	return err
}

func (d *Dispatcher) do(req *http.Request) error {
	req.Header.Set("User-Agent", "Hook-Alerts/0.1")
	resp, err := d.client.Do(req)
	if err != nil {
		return StripURL(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return nil
}

func (d *Dispatcher) telegram(ctx context.Context, c map[string]string, m Message) error {
	text := "<b>" + html.EscapeString(m.Title) + "</b>\n" + html.EscapeString(m.Body)
	if m.URL != "" {
		text += "\n<a href=\"" + html.EscapeString(m.URL) + "\">Open in Hook</a>"
	}
	u := "https://api.telegram.org/bot" + url.PathEscape(c["botToken"]) + "/sendMessage"
	err := d.postJSON(ctx, u, map[string]any{
		"chat_id": c["chatId"], "text": text, "parse_mode": "HTML", "disable_web_page_preview": true,
	}, nil)
	if err != nil {
		// never let the bot token leak into logs
		return errors.New(strings.ReplaceAll(err.Error(), c["botToken"], "***"))
	}
	return nil
}

func (d *Dispatcher) discord(ctx context.Context, c map[string]string, m Message) error {
	embed := map[string]any{"title": m.Title, "description": m.Body, "color": m.Color, "timestamp": m.Event.At.UTC().Format(time.RFC3339)}
	if m.URL != "" {
		embed["url"] = m.URL
	}
	return d.postJSON(ctx, c["webhookUrl"], map[string]any{"username": "Hook", "embeds": []any{embed}}, nil)
}

func (d *Dispatcher) slack(ctx context.Context, c map[string]string, m Message) error {
	text := "*" + m.Title + "*\n" + m.Body
	if m.URL != "" {
		text += "\n<" + m.URL + "|Open in Hook>"
	}
	return d.postJSON(ctx, c["webhookUrl"], map[string]any{"text": text}, nil)
}

// webhook posts a stable JSON payload, signed with HMAC-SHA256 when a secret is set.
// Point it at your own services (e.g. a WhatsApp bot) to route alerts anywhere.
func (d *Dispatcher) webhook(ctx context.Context, c map[string]string, m Message) error {
	ev := m.Event
	payload := map[string]any{
		"event":   ev.Kind,
		"title":   m.Title,
		"message": m.Body,
		"reason":  ev.Msg,
		"time":    ev.At.UTC().Format(time.RFC3339),
		"url":     m.URL,
	}
	// "monitor" or "server", so receivers can route on the key alone
	payload[ev.Source] = map[string]any{"id": ev.ID, "name": ev.Name, "type": ev.Type, "target": ev.Target}
	if ev.Subject != "" {
		payload["subject"] = ev.Subject
	}
	switch ev.Kind {
	case KindUp, KindOnline, KindRecovered, KindContOK, KindServiceOK:
		payload["downtimeSeconds"] = int(ev.Downtime.Seconds())
	case KindCert:
		payload["certDaysLeft"] = ev.CertDays
	}
	if ev.Metric != "" {
		payload["metric"] = map[string]any{"name": ev.Metric, "value": ev.Value, "peak": ev.Peak, "limit": ev.Limit, "minutes": ev.Minutes}
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c["url"], bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hook-Event", ev.Kind)
	if s := c["secret"]; s != "" {
		mac := hmac.New(sha256.New, []byte(s))
		mac.Write(b)
		req.Header.Set("X-Hook-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	return d.do(req)
}

func (d *Dispatcher) ntfy(ctx context.Context, c map[string]string, m Message) error {
	server := strings.TrimRight(c["server"], "/")
	if server == "" {
		server = "https://ntfy.sh"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/"+url.PathEscape(c["topic"]), strings.NewReader(m.Body))
	if err != nil {
		return err
	}
	// ntfy headers must be ASCII-safe; it decodes RFC 2047 encoded words.
	req.Header.Set("Title", "=?UTF-8?B?"+b64(m.Title)+"?=")
	prio, tag := "3", "bell"
	switch m.Event.Kind {
	case KindDown, KindOffline, KindContainer, KindService:
		prio, tag = "5", "rotating_light"
	case KindUp, KindOnline, KindRecovered, KindContOK, KindServiceOK:
		tag = "white_check_mark"
	case KindRestart:
		prio, tag = "4", "repeat"
	case KindCert:
		prio, tag = "4", "lock"
	case KindThreshold:
		prio, tag = "4", "warning"
	}
	req.Header.Set("Priority", prio)
	req.Header.Set("Tags", tag)
	if m.URL != "" {
		req.Header.Set("Click", m.URL)
	}
	if t := c["token"]; t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	return d.do(req)
}

// email sends plain text over SMTP. security: "starttls" (default), "tls" or "none".
func (d *Dispatcher) email(ctx context.Context, c map[string]string, m Message) error {
	addr := net.JoinHostPort(c["host"], c["port"])
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(20 * time.Second)
	}
	dialer := &net.Dialer{Deadline: deadline}
	tlsCfg := &tls.Config{ServerName: c["host"]}

	var conn net.Conn
	var err error
	if c["security"] == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	conn.SetDeadline(deadline)
	cl, err := smtp.NewClient(conn, c["host"])
	if err != nil {
		conn.Close()
		return err
	}
	defer cl.Close()
	if c["security"] == "" || c["security"] == "starttls" {
		if err := cl.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	if c["username"] != "" {
		if err := cl.Auth(smtp.PlainAuth("", c["username"], c["password"], c["host"])); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}
	from := c["from"]
	if err := cl.Mail(addrOnly(from)); err != nil {
		return err
	}
	var to []string
	for _, r := range strings.Split(c["to"], ",") {
		if r = strings.TrimSpace(r); r != "" {
			to = append(to, r)
			if err := cl.Rcpt(addrOnly(r)); err != nil {
				return fmt.Errorf("recipient %s: %w", r, err)
			}
		}
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	body := m.Body
	if m.URL != "" {
		body += "\n\n" + m.URL
	}
	fmt.Fprintf(w, "From: %s\r\nTo: %s\r\nSubject: =?UTF-8?B?%s?=\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n",
		from, strings.Join(to, ", "), b64(m.Title), time.Now().Format(time.RFC1123Z), strings.ReplaceAll(body, "\n", "\r\n"))
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// addrOnly extracts "a@b.c" from "Name <a@b.c>".
func addrOnly(s string) string {
	if i, j := strings.LastIndex(s, "<"), strings.LastIndex(s, ">"); i >= 0 && j > i {
		return s[i+1 : j]
	}
	return strings.TrimSpace(s)
}
