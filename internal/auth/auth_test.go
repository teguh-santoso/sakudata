package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordRoundtrip(t *testing.T) {
	h, err := HashPassword("Rahasia123!")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "argon2id$") {
		t.Fatalf("unexpected format: %s", h)
	}
	if !VerifyPassword(h, "Rahasia123!") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(h, "salah") {
		t.Fatal("wrong password accepted")
	}
}

func TestPasswordTooShort(t *testing.T) {
	if _, err := HashPassword("pendek"); err == nil {
		t.Fatal("short password accepted")
	}
}

func TestJWT(t *testing.T) {
	secret := []byte("test-secret-32-bytes-long-padded!!")
	tok, err := Sign(secret, AdminClaims("admin1", time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	cl, err := Parse(secret, tok)
	if err != nil {
		t.Fatal(err)
	}
	if cl.Sub != "admin1" || cl.Role != "admin" {
		t.Fatalf("bad claims: %+v", cl)
	}
	if _, err := Parse([]byte("wrong-secret"), tok); err == nil {
		t.Fatal("forged secret accepted")
	}
	if _, err := Parse(secret, tok+"x"); err == nil {
		t.Fatal("tampered token accepted")
	}
	expired, _ := Sign(secret, Claims{Sub: "a", Role: "admin", Exp: time.Now().Add(-time.Hour).Unix()})
	if _, err := Parse(secret, expired); err == nil {
		t.Fatal("expired token accepted")
	}
}
