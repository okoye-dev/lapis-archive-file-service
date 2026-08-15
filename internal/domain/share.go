// Package domain holds the core types and rules of Lapis Archive. It depends
// on no other internal package, so every layer (storage, http, workers) can
// import it and the models live in one findable place.
package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const (
	slugLength   = 10
	codeLength   = 6
	saltLength   = 16
	DefaultTTL   = 72 * time.Hour
	MaxTTL       = 7 * 24 * time.Hour
	slugAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	// No 0/O/1/I: avoids characters that are easy to misread when typed by hand.
	codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	// Most access codes a file may have (first share plus two rotations).
	MaxShareCount = 3
)

var (
	ErrExpired     = errors.New("share expired")
	ErrWrongCode   = errors.New("wrong access code")
	ErrInvalidSlug = errors.New("invalid slug")
	ErrNotFound    = errors.New("share not found")
	ErrShareLimit  = errors.New("share limit reached")
)

// Share is a file made shareable behind a one-time access code.
type Share struct {
	Slug           string    `json:"slug"`
	StorageKey     string    `json:"storage_key"`
	FileName       string    `json:"file_name"`
	FileSize       int64     `json:"file_size"`
	OwnerID        string    `json:"owner_id,omitempty"`
	OwnerEmail     string    `json:"owner_email,omitempty"`
	RecipientEmail string    `json:"recipient_email,omitempty"`
	CodeSalt       string    `json:"code_salt"`
	CodeHash       string    `json:"code_hash"`
	ShareCount     int       `json:"share_count"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// NewShare builds a share and returns it alongside the plaintext access code,
// which is shown to the creator once and never stored (only its salted hash).
func NewShare(storageKey, fileName string, fileSize int64, ownerID, ownerEmail, recipientEmail string, ttl time.Duration) (*Share, string, error) {
	if ttl <= 0 || ttl > MaxTTL {
		ttl = DefaultTTL
	}

	slug, err := randomString(slugAlphabet, slugLength)
	if err != nil {
		return nil, "", fmt.Errorf("generating slug: %w", err)
	}

	code, err := randomString(codeAlphabet, codeLength)
	if err != nil {
		return nil, "", fmt.Errorf("generating code: %w", err)
	}

	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return nil, "", fmt.Errorf("generating salt: %w", err)
	}

	now := time.Now().UTC()
	share := &Share{
		Slug:           slug,
		StorageKey:     storageKey,
		FileName:       fileName,
		FileSize:       fileSize,
		OwnerID:        ownerID,
		OwnerEmail:     ownerEmail,
		RecipientEmail: recipientEmail,
		CodeSalt:       hex.EncodeToString(salt),
		CodeHash:       hashCode(salt, code),
		ShareCount:     1,
		CreatedAt:      now,
		ExpiresAt:      now.Add(ttl),
	}

	return share, code, nil
}

// Rotate swaps in a new code and expiry, keeping the slug. Fails with
// ErrShareLimit once the file has had MaxShareCount codes.
func (s *Share) Rotate(ttl time.Duration) (string, error) {
	if s.ShareCount >= MaxShareCount {
		return "", ErrShareLimit
	}
	if ttl <= 0 || ttl > MaxTTL {
		ttl = DefaultTTL
	}

	code, err := randomString(codeAlphabet, codeLength)
	if err != nil {
		return "", fmt.Errorf("generating code: %w", err)
	}

	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating salt: %w", err)
	}

	s.CodeSalt = hex.EncodeToString(salt)
	s.CodeHash = hashCode(salt, code)
	s.ShareCount++
	s.ExpiresAt = time.Now().UTC().Add(ttl)

	return code, nil
}

// Verify reports whether code matches (constant-time), tolerating spaces,
// dashes, and lowercase in what the recipient typed.
func (s *Share) Verify(code string) error {
	if time.Now().UTC().After(s.ExpiresAt) {
		return ErrExpired
	}

	salt, err := hex.DecodeString(s.CodeSalt)
	if err != nil {
		return fmt.Errorf("decoding salt: %w", err)
	}

	expected := []byte(s.CodeHash)
	actual := []byte(hashCode(salt, normalizeCode(code)))
	if subtle.ConstantTimeCompare(expected, actual) != 1 {
		return ErrWrongCode
	}

	return nil
}

func (s *Share) Expired() bool {
	return time.Now().UTC().After(s.ExpiresAt)
}

// ValidateSlug guards slugs taken from the URL before they reach storage.
func ValidateSlug(slug string) error {
	if len(slug) != slugLength {
		return ErrInvalidSlug
	}
	for _, r := range slug {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return ErrInvalidSlug
		}
	}
	return nil
}

func normalizeCode(code string) string {
	out := make([]rune, 0, len(code))
	for _, r := range code {
		if r == ' ' || r == '-' {
			continue
		}
		if r >= 'a' && r <= 'z' {
			r -= 32
		}
		out = append(out, r)
	}
	return string(out)
}

func hashCode(salt []byte, code string) string {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(code))
	return hex.EncodeToString(h.Sum(nil))
}

func randomString(alphabet string, length int) (string, error) {
	out := make([]byte, length)
	max := 256 - (256 % len(alphabet))
	buf := make([]byte, 1)
	for i := 0; i < length; {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		if int(buf[0]) >= max {
			continue
		}
		out[i] = alphabet[int(buf[0])%len(alphabet)]
		i++
	}
	return string(out), nil
}
