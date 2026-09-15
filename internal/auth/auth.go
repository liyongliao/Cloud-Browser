package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"golang.org/x/crypto/argon2"
	"strings"
)

func Token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func ID() string { return hex.EncodeToString(random(16)) }
func random(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}
func Digest(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func Password(s string) (string, error) {
	if len(s) < 12 || len(s) > 256 {
		return "", errors.New("password must be 12–256 bytes")
	}
	salt := random(16)
	key := argon2.IDKey([]byte(s), salt, 2, 64*1024, 2, 32)
	return "a2id$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}
func Verify(hash, s string) bool {
	if len(s) > 256 {
		return false
	}
	parts := strings.Split(hash, "$")
	if len(parts) != 3 || parts[0] != "a2id" {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(parts[1])
	if e != nil || len(salt) != 16 {
		return false
	}
	expected, e := base64.RawStdEncoding.DecodeString(parts[2])
	if e != nil || len(expected) != 32 {
		return false
	}
	actual := argon2.IDKey([]byte(s), salt, 2, 64*1024, 2, 32)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
func Equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
