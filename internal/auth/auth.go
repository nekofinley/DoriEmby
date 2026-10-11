package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

const pbkdf2Iters = 60000

// HashPassword derives a PBKDF2-SHA256 hash (stdlib crypto/pbkdf2, Go 1.24+).
func HashPassword(password string) (string, string) {
	salt := randomBytes(16)
	key := derive(password, salt)
	return hex.EncodeToString(key), hex.EncodeToString(salt)
}

func derive(password string, salt []byte) []byte {
	key, err := pbkdf2Key(password, salt)
	if err != nil {
		panic(err)
	}
	return key
}

func Verify(password, hashHex, saltHex string) bool {
	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(hashHex)
	if err != nil {
		return false
	}
	got := derive(password, salt)
	return hmac.Equal(want, got)
}

func NewToken() string {
	return hex.EncodeToString(randomBytes(32))
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// Sign builds an HMAC-SHA256 signature for URL signing.
func Sign(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifySig(secret, payload, sig string) bool {
	want := Sign(secret, payload)
	return hmac.Equal([]byte(want), []byte(sig))
}
