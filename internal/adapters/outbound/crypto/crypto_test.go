package crypto

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, KeyBytes) }

func TestSealerRoundTripsWithAFreshNonceAndHidesThePlaintext(t *testing.T) {
	s, err := NewSealer(key(7))
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(`{"line1":"10 Sample Street"}`)
	a, err := s.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.Seal(plain)
	if bytes.Equal(a.Nonce, b.Nonce) || bytes.Equal(a.Ciphertext, b.Ciphertext) {
		t.Fatal("every seal needs a fresh nonce")
	}
	if bytes.Contains(a.Ciphertext, []byte("Sample")) || a.KeyID != s.KeyID() || len(a.KeyID) != 8 || len(a.Nonce) != 12 {
		t.Fatalf("sealed = %+v", a)
	}
	got, err := s.Open(a)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("open: %q %v", got, err)
	}
}

func TestSealerRefusesTamperingAndAForeignKey(t *testing.T) {
	s, _ := NewSealer(key(7))
	other, _ := NewSealer(key(8))
	sealed, _ := s.Seal([]byte("x"))
	if _, err := other.Open(sealed); !errors.Is(err, ErrBadKey) {
		t.Fatalf("foreign key: %v", err)
	}
	sealed.Ciphertext[0] ^= 1
	if _, err := s.Open(sealed); err == nil {
		t.Fatal("tampered ciphertext must not open")
	}
	if s.KeyID() == other.KeyID() {
		t.Fatal("different keys have different ids")
	}
}

func TestNewSealerRefusesAWrongKeyLength(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33} {
		if _, err := NewSealer(make([]byte, n)); !errors.Is(err, ErrBadKey) {
			t.Errorf("%d bytes: %v", n, err)
		}
	}
}

func TestDecodeKey(t *testing.T) {
	raw := key(9)
	for name, enc := range map[string]string{
		"std": base64.StdEncoding.EncodeToString(raw), "raw": base64.RawStdEncoding.EncodeToString(raw),
		"url": base64.URLEncoding.EncodeToString(raw), "rawurl": base64.RawURLEncoding.EncodeToString(raw),
	} {
		got, err := DecodeKey(enc)
		if err != nil || !bytes.Equal(got, raw) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := DecodeKey(base64.StdEncoding.EncodeToString(make([]byte, 16))); !errors.Is(err, ErrBadKey) || !strings.Contains(err.Error(), "16") {
		t.Errorf("short key: %v", err)
	}
	if _, err := DecodeKey("%%%"); !errors.Is(err, ErrBadKey) {
		t.Errorf("not base64: %v", err)
	}
}

func TestStopKeyIsAKeyedDeterministicHexHash(t *testing.T) {
	a, err := NewStopKeyer([]byte("0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewStopKeyer([]byte("fedcba9876543210"))
	k1, k2 := a.StopKey("10SAMPLE|X"), a.StopKey("10SAMPLE|X")
	if k1 != k2 || len(k1) != 64 || strings.Trim(k1, "0123456789abcdef") != "" {
		t.Fatalf("key = %q", k1)
	}
	if a.StopKey("10SAMPLE|Y") == k1 || b.StopKey("10SAMPLE|X") == k1 {
		t.Fatal("another address or another secret gives another key")
	}
	// Known answer: HMAC-SHA-256 with key "0123456789abcdef" over "abc".
	if got := a.StopKey("abc"); got != "bcaa92843247bc258080d2824bddeba4afb1bd61b4688ca65f60a214e1d9759e" {
		t.Fatalf("unexpected %q", got)
	}
}

func TestNewStopKeyerRefusesAShortSecretAndCopiesItsInput(t *testing.T) {
	if _, err := NewStopKeyer([]byte("short")); !errors.Is(err, ErrBadKey) {
		t.Fatalf("short secret: %v", err)
	}
	secret := []byte("0123456789abcdef")
	k, _ := NewStopKeyer(secret)
	before := k.StopKey("x")
	secret[0] = 'Z'
	if k.StopKey("x") != before {
		t.Fatal("the keyer must copy its secret")
	}
	if _, err := NewStopKeyer(make([]byte, MinStopSecretBytes)); err != nil {
		t.Fatalf("exactly the minimum is accepted: %v", err)
	}
}
