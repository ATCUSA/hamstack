package web

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// passwordIterations makes one login check cost about a tenth of a second on a Raspberry Pi 4: cheap for a
// person, slow for anyone guessing.
const passwordIterations = 210000

// HashPassword returns "pbkdf2-sha256$ITERATIONS$SALT$KEY" (salt and key in base64).
func HashPassword(pw string) (string, error) {
	if pw == "" {
		return "", errors.New("the password is empty")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, passwordIterations, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", passwordIterations,
		base64.StdEncoding.EncodeToString(salt), base64.StdEncoding.EncodeToString(key)), nil
}

// CheckPassword reports whether pw matches a hash made by HashPassword.
func CheckPassword(hash, pw string) bool {
	parts := strings.Split(strings.TrimSpace(hash), "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1000 || iter > 10000000 {
		return false
	}
	salt, err1 := base64.StdEncoding.DecodeString(parts[2])
	want, err2 := base64.StdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// SavePasswordHash writes the hash to file, readable by its owner only.
func SavePasswordHash(file, hash string) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".web-password-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(hash + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}

// readPasswordHash returns the saved hash, or "" when no password has been set.
func readPasswordHash(file string) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
