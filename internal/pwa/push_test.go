package pwa

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hook/internal/secret"
	"hook/internal/store"
)

func hmacSHA(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// decrypt does what the browser does with an aes128gcm Web Push body (RFC 8291).
func decrypt(t *testing.T, body []byte, priv *ecdh.PrivateKey, auth []byte) []byte {
	t.Helper()
	salt, rs, idlen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	senderPub := body[21 : 21+idlen]
	ct := body[21+idlen:]
	if rs < 18 || idlen != 65 {
		t.Fatalf("bad header rs=%d idlen=%d", rs, idlen)
	}
	sp, err := ecdh.P256().NewPublicKey(senderPub)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := priv.ECDH(sp)
	if err != nil {
		t.Fatal(err)
	}
	prkKey := hmacSHA(auth, shared)
	info := append(append([]byte("WebPush: info\x00"), priv.PublicKey().Bytes()...), senderPub...)
	ikm := hmacSHA(prkKey, append(info, 1))
	prk := hmacSHA(salt, ikm)
	cek := hmacSHA(prk, []byte("Content-Encoding: aes128gcm\x00\x01"))[:16]
	nonce := hmacSHA(prk, []byte("Content-Encoding: nonce\x00\x01"))[:12]
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	i := len(plain) - 1
	for i >= 0 && plain[i] == 0 {
		i--
	}
	if i < 0 || plain[i] != 2 {
		t.Fatal("missing final-record delimiter")
	}
	return plain[:i]
}

func TestSendEncryptsForTheDevice(t *testing.T) {
	dir := t.TempDir()
	box, _ := secret.Load("", dir)
	st, err := store.Open(dir, box)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, _ := st.CreateUser("admin", "x")

	// The "browser": a P-256 key pair and auth secret, as PushManager makes.
	priv, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	rand.Read(auth)

	var got Notification
	var vapid string
	gone := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/gone") {
			gone = true
			w.WriteHeader(http.StatusGone)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Content-Encoding") != "aes128gcm" {
			t.Errorf("content encoding %q", r.Header.Get("Content-Encoding"))
		}
		vapid = r.Header.Get("Authorization")
		json.Unmarshal(decrypt(t, body, priv, auth), &got)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p, err := New(st, "https://hook.example.com", log)
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.RawURLEncoding.EncodeToString
	st.SavePushSubscription(&store.PushSubscription{UserID: u.ID, Endpoint: srv.URL + "/push/abc", P256dh: b64(priv.PublicKey().Bytes()), Auth: b64(auth), Label: "phone"})
	st.SavePushSubscription(&store.PushSubscription{UserID: u.ID, Endpoint: srv.URL + "/push/gone", P256dh: b64(priv.PublicKey().Bytes()), Auth: b64(auth), Label: "old laptop"})

	sent, err := p.Send(context.Background(), Notification{Title: "🔴 API is DOWN", Body: "Connection refused", URL: "/monitors/3", Tag: "monitor-3"})
	if err != nil || sent != 1 {
		t.Fatalf("sent=%d err=%v", sent, err)
	}
	if got.Title != "🔴 API is DOWN" || got.URL != "/monitors/3" {
		t.Fatalf("decrypted payload: %+v", got)
	}
	if !strings.HasPrefix(vapid, "vapid t=") || !strings.Contains(vapid, "k="+p.PublicKey()) {
		t.Fatalf("VAPID header: %q", vapid)
	}
	subs, _ := st.PushSubscriptions()
	if !gone || len(subs) != 1 || subs[0].Label != "phone" {
		t.Fatalf("a 410 should remove that device; left %d", len(subs))
	}

	// The key pair survives a restart; otherwise every device would break.
	p2, _ := New(st, "https://hook.example.com", log)
	if p2.PublicKey() != p.PublicKey() {
		t.Fatal("VAPID key changed between runs")
	}
}
