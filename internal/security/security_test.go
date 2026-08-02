package security

import "testing"

func TestVaultAndSession(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	v, err := NewVault(key)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := v.Encrypt("sk-secret")
	if err != nil || enc == "sk-secret" {
		t.Fatal("secret was not encrypted")
	}
	plain, err := v.Decrypt(enc)
	if err != nil || plain != "sk-secret" {
		t.Fatalf("decrypt=%q err=%v", plain, err)
	}
	hash, err := HashPassword("password-123")
	if err != nil || !CheckPassword(hash, "password-123") || CheckPassword(hash, "wrong-password") {
		t.Fatal("password verification failed")
	}
	token, err := SignSession(key, 42, 60_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	id, err := VerifySession(key, token)
	if err != nil || id != 42 {
		t.Fatalf("session id=%d err=%v", id, err)
	}
}
