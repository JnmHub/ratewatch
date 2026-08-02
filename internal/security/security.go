package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Vault struct{ aead cipher.AEAD }

func NewVault(key []byte) (*Vault, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	a, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: a}, nil
}

func (v *Vault) Encrypt(plain string) (string, error) {
	n := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(n); err != nil {
		return "", err
	}
	sealed := v.aead.Seal(n, n, []byte(plain), nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (v *Vault) Decrypt(encoded string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(b) < v.aead.NonceSize() {
		return "", errors.New("invalid encrypted secret")
	}
	p, err := v.aead.Open(nil, b[:v.aead.NonceSize()], b[v.aead.NonceSize():], nil)
	return string(p), err
}

func HashPassword(password string) (string, error) {
	if len(password) < 6 {
		return "", errors.New("密码至少需要 6 位")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	h := stretch([]byte(password), salt, 120000)
	return fmt.Sprintf("%d.%s.%s", 120000, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(h)), nil
}

func CheckPassword(encoded, password string) bool {
	p := strings.Split(encoded, ".")
	if len(p) != 3 {
		return false
	}
	rounds, _ := strconv.Atoi(p[0])
	salt, e1 := base64.RawStdEncoding.DecodeString(p[1])
	expected, e2 := base64.RawStdEncoding.DecodeString(p[2])
	if e1 != nil || e2 != nil || rounds < 1 {
		return false
	}
	return hmac.Equal(expected, stretch([]byte(password), salt, rounds))
}

func stretch(password, salt []byte, rounds int) []byte {
	h := sha256.Sum256(append(append([]byte{}, salt...), password...))
	b := h[:]
	for i := 1; i < rounds; i++ {
		x := sha256.Sum256(append(append([]byte{}, b...), password...))
		b = x[:]
	}
	return b
}

type claims struct {
	UserID int64 `json:"uid"`
	Exp    int64 `json:"exp"`
}

func SignSession(secret []byte, userID int64, ttl time.Duration) (string, error) {
	b, err := json.Marshal(claims{UserID: userID, Exp: time.Now().Add(ttl).Unix()})
	if err != nil {
		return "", err
	}
	p := base64.RawURLEncoding.EncodeToString(b)
	m := hmac.New(sha256.New, secret)
	_, _ = m.Write([]byte(p))
	return p + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil)), nil
}

func VerifySession(secret []byte, token string) (int64, error) {
	p := strings.Split(token, ".")
	if len(p) != 2 {
		return 0, errors.New("invalid token")
	}
	m := hmac.New(sha256.New, secret)
	_, _ = m.Write([]byte(p[0]))
	sig, err := base64.RawURLEncoding.DecodeString(p[1])
	if err != nil || !hmac.Equal(sig, m.Sum(nil)) {
		return 0, errors.New("invalid token")
	}
	b, err := base64.RawURLEncoding.DecodeString(p[0])
	var c claims
	if err != nil || json.Unmarshal(b, &c) != nil || c.Exp < time.Now().Unix() {
		return 0, errors.New("expired token")
	}
	return c.UserID, nil
}

func MaskSecret(s string) string {
	if len(s) <= 8 {
		return "••••••••"
	}
	return s[:3] + "••••••••" + s[len(s)-4:]
}

func Fingerprint(baseURL, secret string) string {
	h := sha256.Sum256([]byte(strings.ToLower(strings.TrimRight(strings.TrimSpace(baseURL), "/")) + "\x00" + secret))
	return hex.EncodeToString(h[:])
}
