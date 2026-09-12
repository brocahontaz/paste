package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func samplePaste(id string) *Paste {
	return &Paste{
		ID:              id,
		Content:         []byte("hello"),
		Format:          "text",
		Language:        "",
		Burn:            false,
		DeleteTokenHash: "hash-" + id,
		CreatedAt:       time.Now().Unix(),
	}
}

func TestCreateGetDeleteRoundtrip(t *testing.T) {
	st := newTestStore(t)
	p := samplePaste("abcdefghij")
	if err := st.Create(p); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := st.Get("abcdefghij")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != p.ID || string(got.Content) != "hello" || got.Format != "text" ||
		got.Language != "" || got.Burn || got.DeleteTokenHash != p.DeleteTokenHash {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if got.ExpiresAt != nil {
		t.Fatalf("expires_at = %v, want NULL", got.ExpiresAt)
	}

	// Wrong delete token must not delete.
	if err := st.DeleteWithToken("abcdefghij", "wrong-hash"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete with wrong token: err = %v, want ErrNotFound", err)
	}
	if _, err := st.Get("abcdefghij"); err != nil {
		t.Fatalf("paste should survive wrong-token delete: %v", err)
	}

	// Correct hash deletes.
	if err := st.DeleteWithToken("abcdefghij", p.DeleteTokenHash); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.Get("abcdefghij"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: err = %v, want ErrNotFound", err)
	}
	// Deleting again is still "not found", never a distinct "wrong token".
	if err := st.DeleteWithToken("abcdefghij", p.DeleteTokenHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete after delete: err = %v, want ErrNotFound", err)
	}
}

func TestCreateConflict(t *testing.T) {
	st := newTestStore(t)
	if err := st.Create(samplePaste("abcdefghij")); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := st.Create(samplePaste("abcdefghij")); !errors.Is(err, ErrConflict) {
		t.Fatalf("second create: err = %v, want ErrConflict", err)
	}
}

func TestGetUnknown(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Get("zzzzzzzzzz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get unknown: err = %v, want ErrNotFound", err)
	}
}

func TestDeleteExpired(t *testing.T) {
	st := newTestStore(t)
	now := time.Now().Unix()

	past := now - 10
	if err := st.Create(&Paste{ID: "expired0001", Content: []byte("x"), Format: "text",
		DeleteTokenHash: "h1", CreatedAt: past, ExpiresAt: &past}); err != nil {
		t.Fatalf("create expired: %v", err)
	}
	future := now + 3600
	if err := st.Create(&Paste{ID: "live0000001", Content: []byte("x"), Format: "text",
		DeleteTokenHash: "h2", CreatedAt: now, ExpiresAt: &future}); err != nil {
		t.Fatalf("create live: %v", err)
	}
	if err := st.Create(&Paste{ID: "never000001", Content: []byte("x"), Format: "text",
		DeleteTokenHash: "h3", CreatedAt: now, ExpiresAt: nil}); err != nil {
		t.Fatalf("create never: %v", err)
	}

	n, err := st.DeleteExpired(now)
	if err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged %d rows, want 1", n)
	}
	if _, err := st.Get("expired0001"); !errors.Is(err, ErrNotFound) {
		t.Error("expired paste survived purge")
	}
	if _, err := st.Get("live0000001"); err != nil {
		t.Errorf("live paste was purged: %v", err)
	}
	if _, err := st.Get("never000001"); err != nil {
		t.Errorf("never-expiring paste was purged: %v", err)
	}
}

func TestPing(t *testing.T) {
	st := newTestStore(t)
	if err := st.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
}
