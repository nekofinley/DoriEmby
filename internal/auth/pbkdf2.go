package auth

import (
	"crypto/pbkdf2"
	"crypto/sha256"
)

func pbkdf2Key(password string, salt []byte) ([]byte, error) {
	return pbkdf2.Key(sha256.New, password, salt, pbkdf2Iters, 32)
}
