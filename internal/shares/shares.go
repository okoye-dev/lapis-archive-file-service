package shares

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
	KeyPrefix    = "shares/"
	slugLength   = 10
	codeLength   = 6
	saltLength   = 16
	DefaultTTL   = 72 * time.Hour
	MaxTTL       = 7 * 24 * time.Hour
	slugAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

var (
	ErrExpired     = errors.New("share expired")
	ErrWrongCode   = errors.New("wrong access code")
	ErrInvalidSlug = errors.New("invalid slug")
)

type Share struct {
	Slug           string    `json:"slug"`
	StorageKey     string    `json:"storage_key"`
	FileName       string    `json:"file_name"`
	FileSize       int64     `json:"file_size"`
	RecipientEmail string    `json:"recipient_email,omitempty"`
	CodeSalt       string    `json:"code_salt"`
	CodeHash       string    `json:"code_hash"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

func New(storageKey, fileName string, fileSize int64, recipientEmail string, ttl time.Duration) (*Share, string, error) {
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
		RecipientEmail: recipientEmail,
		CodeSalt:       hex.EncodeToString(salt),
		CodeHash:       hashCode(salt, code),
		CreatedAt:      now,
		ExpiresAt:      now.Add(ttl),
	}

	return share, code, nil
}

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

func StorageKeyFor(slug string) (string, error) {
	if !validSlug(slug) {
		return "", ErrInvalidSlug
	}
	return KeyPrefix + slug + ".json", nil
}

func validSlug(slug string) bool {
	if len(slug) != slugLength {
		return false
	}
	for _, r := range slug {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return true
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
