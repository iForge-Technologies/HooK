// Package auth holds password hashing, TOTP and login rate limiting.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters (OWASP baseline: 19 MiB, 2 passes).
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
)

var b64 = base64.RawStdEncoding

// HashSlots bounds concurrent argon2id runs. Each holds 19 MiB, so without a
// cap a burst of logins could push the hub past its memory limit.
const HashSlots = 4

var hashSlots = make(chan struct{}, HashSlots)

func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hashSlots <- struct{}{}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	<-hashSlots
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// CheckPassword waits for a free hash slot. Signed-in callers use it.
func CheckPassword(pw, encoded string) bool {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	return checkPassword(pw, encoded)
}

// TryCheckPassword is for unauthenticated callers: rather than queueing behind
// a flood, it reports busy when every slot is taken.
func TryCheckPassword(pw, encoded string) (match, busy bool) {
	select {
	case hashSlots <- struct{}{}:
	default:
		return false, true
	}
	defer func() { <-hashSlots }()
	return checkPassword(pw, encoded), false
}

func checkPassword(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash lets failed username lookups spend the same time as real checks.
var dummyHash, _ = HashPassword("timing-equaliser")

// TryCheckDummy spends a real check's time on an unknown username; busy as in TryCheckPassword.
func TryCheckDummy(pw string) (busy bool) {
	_, busy = TryCheckPassword(pw, dummyHash)
	return busy
}

func ValidatePassword(pw string) error {
	if len(pw) < 10 {
		return errors.New("password must be at least 10 characters")
	}
	if len(pw) > 256 {
		return errors.New("password is too long")
	}
	return nil
}
