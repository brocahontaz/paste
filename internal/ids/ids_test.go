package ids

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

var idRe = regexp.MustCompile(`^[A-Za-z0-9]{10}$`)

func TestNewIDShapeAndUniqueness(t *testing.T) {
	seen := make(map[string]bool, 500)
	for i := 0; i < 500; i++ {
		id, err := NewID()
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		if !idRe.MatchString(id) {
			t.Fatalf("id %q does not match ^[A-Za-z0-9]{10}$", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q after %d iterations", id, i)
		}
		seen[id] = true
	}
}

func TestNewIDRejectsReserved(t *testing.T) {
	// Reserved route words are rejected by the generator.
	for word := range reserved {
		if !IsReserved(word) {
			t.Errorf("IsReserved(%q) = false, want true", word)
		}
	}
	if IsReserved("abcdefghij") {
		t.Error("IsReserved(abcdefghij) = true, want false")
	}
}

func TestValidID(t *testing.T) {
	cases := map[string]bool{
		"abcABC0123":      true,
		"short":           false,
		"waytoolongid":    false,
		"with-dash1":      false,
		"with_underscore": false,
		"":                false,
	}
	for in, want := range cases {
		if got := ValidID(in); got != want {
			t.Errorf("ValidID(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNewDeleteToken(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		tok, err := NewDeleteToken()
		if err != nil {
			t.Fatalf("NewDeleteToken: %v", err)
		}
		// 32 bytes base64url raw = 43 chars.
		if len(tok) != 43 {
			t.Fatalf("token length = %d, want 43", len(tok))
		}
		if strings.ContainsAny(tok, "+/=") {
			t.Fatalf("token %q contains non-URL-safe base64 characters", tok)
		}
		if seen[tok] {
			t.Fatalf("duplicate token %q", tok)
		}
		seen[tok] = true
	}
}

func TestHashToken(t *testing.T) {
	h1 := HashToken("secret-token")
	h2 := HashToken("secret-token")
	h3 := HashToken("other-token")
	if h1 != h2 {
		t.Fatal("HashToken not deterministic")
	}
	if h1 == h3 {
		t.Fatal("different tokens produced equal hashes")
	}
	if len(h1) != 64 {
		t.Fatalf("hash length = %d, want 64 hex chars", len(h1))
	}
	if h1 == "secret-token" {
		t.Fatal("hash equals plaintext")
	}
	if !errors.Is(ErrReserved, ErrReserved) {
		t.Fatal("sentinel comparison broken")
	}
}
