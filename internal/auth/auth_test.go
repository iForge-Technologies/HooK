package auth

import (
	"testing"
	"time"
)

// RFC 6238 appendix B vectors (SHA-1), truncated to 6 digits.
func TestTOTPVectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	for _, tc := range []struct {
		unix int64
		code string
	}{{59, "287082"}, {1111111109, "081804"}, {1234567890, "005924"}, {2000000000, "279037"}} {
		if got := hotp(secret, tc.unix/30); got != tc.code {
			t.Errorf("t=%d: got %s want %s", tc.unix, got, tc.code)
		}
		if _, ok := VerifyTOTP(secret, tc.code, time.Unix(tc.unix+30, 0)); !ok {
			t.Errorf("t=%d: code should verify one step later", tc.unix)
		}
		if _, ok := VerifyTOTP(secret, tc.code, time.Unix(tc.unix+90, 0)); ok {
			t.Errorf("t=%d: code should be rejected three steps later", tc.unix)
		}
	}
}

func TestPassword(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword("correct horse battery", h) || CheckPassword("wrong", h) {
		t.Fatal("password check mismatch")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(2, time.Minute)
	l.Fail("a")
	if l.Blocked("a") {
		t.Fatal("blocked too early")
	}
	l.Fail("a")
	if !l.Blocked("a") || l.Blocked("b") {
		t.Fatal("limiter keys wrong")
	}
	l.Reset("a")
	if l.Blocked("a") {
		t.Fatal("reset didn't clear")
	}
}
