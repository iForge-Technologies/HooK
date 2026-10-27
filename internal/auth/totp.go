package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// RFC 6238 TOTP: SHA-1, 6 digits, 30 s steps — what every authenticator app expects.

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func NewTOTPSecret() []byte {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func TOTPSecretString(secret []byte) string { return b32.EncodeToString(secret) }

func TOTPURI(secret []byte, issuer, account string) string {
	v := url.Values{}
	v.Set("secret", TOTPSecretString(secret))
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", "6")
	v.Set("period", "30")
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + v.Encode()
}

func hotp(secret []byte, counter int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", code%1_000_000)
}

// VerifyTOTP checks a code against the current step ±1 and returns the matching
// step, so the caller can reject reuse of the same code.
func VerifyTOTP(secret []byte, code string, now time.Time) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return 0, false
	}
	step := now.Unix() / 30
	for d := int64(-1); d <= 1; d++ {
		if hmac.Equal([]byte(hotp(secret, step+d)), []byte(code)) {
			return step + d, true
		}
	}
	return 0, false
}
