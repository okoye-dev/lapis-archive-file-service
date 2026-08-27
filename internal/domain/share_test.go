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

func TestRotate(t *testing.T) {
	share, first, err := NewShare("k_f.txt", "f.txt", 1, "", "", "", 1*time.Hour)
	if err != nil {
		t.Fatalf("NewShare: %v", err)
	}
	if share.ShareCount != 1 {
		t.Fatalf("initial ShareCount = %d, want 1", share.ShareCount)
	}
	slug, firstExpiry := share.Slug, share.ExpiresAt

	second, err := share.Rotate(3 * time.Hour)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if second == first {
		t.Error("rotate returned the same code")
	}
	if share.Slug != slug {
		t.Error("rotate changed the slug")
	}
	if share.ShareCount != 2 {
		t.Errorf("ShareCount = %d, want 2", share.ShareCount)
	}
	if !share.ExpiresAt.After(firstExpiry) {
		t.Error("rotate did not extend the expiry")
	}
	if err := share.Verify(first); !errors.Is(err, ErrWrongCode) {
		t.Errorf("old code Verify = %v, want ErrWrongCode", err)
	}
	if err := share.Verify(second); err != nil {
		t.Errorf("new code Verify = %v, want nil", err)
	}
}

func TestRotateClampsTTL(t *testing.T) {
	cases := []struct{ ttl, want time.Duration }{
		{0, DefaultTTL},
		{30 * 24 * time.Hour, DefaultTTL},
		{2 * time.Hour, 2 * time.Hour},
	}
	for _, c := range cases {
		share, _, _ := NewShare("k_f.txt", "f.txt", 1, "", "", "", 0)
		now := time.Now().UTC()
		if _, err := share.Rotate(c.ttl); err != nil {
			t.Fatalf("Rotate(%v): %v", c.ttl, err)
		}
		got := share.ExpiresAt.Sub(now)
		if got < c.want-time.Minute || got > c.want+time.Minute {
			t.Errorf("Rotate(%v) expiry in %v, want ~%v", c.ttl, got, c.want)
		}
	}
}

func TestRotateShareLimit(t *testing.T) {
	share, _, _ := NewShare("k_f.txt", "f.txt", 1, "", "", "", 0) // count 1
	if _, err := share.Rotate(0); err != nil {                    // count 2
		t.Fatalf("rotate 1: %v", err)
	}
	if _, err := share.Rotate(0); err != nil { // count 3
		t.Fatalf("rotate 2: %v", err)
	}
	code, err := share.Rotate(0) // would be 4: over the cap
	if !errors.Is(err, ErrShareLimit) {
		t.Errorf("third rotate = %v, want ErrShareLimit", err)
	}
	if code != "" {
		t.Errorf("code at cap = %q, want empty", code)
	}
	if share.ShareCount != MaxShareCount {
		t.Errorf("ShareCount = %d, want %d unchanged at cap", share.ShareCount, MaxShareCount)
	}
}
