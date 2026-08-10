package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNewAndVerify(t *testing.T) {
	share, code, err := NewShare("abc_photo.jpg", "photo.jpg", 1234, "", "", "", 0)
	if err != nil {
		t.Fatalf("NewShare: %v", err)
	}

	if len(share.Slug) != slugLength {
		t.Errorf("slug length = %d, want %d", len(share.Slug), slugLength)
	}
	if len(code) != codeLength {
		t.Errorf("code length = %d, want %d", len(code), codeLength)
	}
	if strings.Contains(share.CodeHash, code) {
		t.Error("code stored in plain text")
	}

	if err := share.Verify(code); err != nil {
		t.Errorf("Verify(correct code): %v", err)
	}
	if err := share.Verify("WRONG1"); !errors.Is(err, ErrWrongCode) {
		t.Errorf("Verify(wrong code) = %v, want ErrWrongCode", err)
	}
}

func TestVerifyNormalizesInput(t *testing.T) {
	share, code, err := NewShare("k_f.txt", "f.txt", 1, "", "", "", 0)
	if err != nil {
		t.Fatalf("NewShare: %v", err)
	}

	spaced := " " + strings.ToLower(code[:3]) + "-" + code[3:] + " "
	if err := share.Verify(spaced); err != nil {
		t.Errorf("Verify(%q): %v", spaced, err)
	}
}

func TestVerifyExpired(t *testing.T) {
	share, code, err := NewShare("k_f.txt", "f.txt", 1, "", "", "", 0)
	if err != nil {
		t.Fatalf("NewShare: %v", err)
	}
	share.ExpiresAt = time.Now().UTC().Add(-time.Minute)

	if err := share.Verify(code); !errors.Is(err, ErrExpired) {
		t.Errorf("Verify(expired) = %v, want ErrExpired", err)
	}
	if !share.Expired() {
		t.Error("Expired() = false, want true")
	}
}

func TestTTLBounds(t *testing.T) {
	share, _, _ := NewShare("k_f.txt", "f.txt", 1, "", "", "", 0)
	if got := share.ExpiresAt.Sub(share.CreatedAt); got != DefaultTTL {
		t.Errorf("default ttl = %v, want %v", got, DefaultTTL)
	}

	share, _, _ = NewShare("k_f.txt", "f.txt", 1, "", "", "", 30*24*time.Hour)
	if got := share.ExpiresAt.Sub(share.CreatedAt); got != DefaultTTL {
		t.Errorf("oversized ttl = %v, want %v", got, DefaultTTL)
	}

	share, _, _ = NewShare("k_f.txt", "f.txt", 1, "", "", "", 2*time.Hour)
	if got := share.ExpiresAt.Sub(share.CreatedAt); got != 2*time.Hour {
		t.Errorf("custom ttl = %v, want 2h", got)
	}
}

func TestValidateSlug(t *testing.T) {
	share, _, _ := NewShare("k_f.txt", "f.txt", 1, "", "", "", 0)
	if err := ValidateSlug(share.Slug); err != nil {
		t.Errorf("ValidateSlug(%q): %v", share.Slug, err)
	}

	for _, bad := range []string{"", "short", "../../etc/passwd", "abc/def.js", strings.Repeat("a", 11)} {
		if err := ValidateSlug(bad); !errors.Is(err, ErrInvalidSlug) {
			t.Errorf("ValidateSlug(%q) = %v, want ErrInvalidSlug", bad, err)
		}
	}
}

func TestCodeAlphabetAvoidsAmbiguity(t *testing.T) {
	for _, r := range "01IO" {
		if strings.ContainsRune(codeAlphabet, r) {
			t.Errorf("ambiguous char %q in code alphabet", r)
		}
	}
}

func TestUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 200; i++ {
		share, _, err := NewShare("k_f.txt", "f.txt", 1, "", "", "", 0)
		if err != nil {
			t.Fatalf("NewShare: %v", err)
		}
		if seen[share.Slug] {
			t.Fatalf("duplicate slug %q", share.Slug)
		}
		seen[share.Slug] = true
	}
}
