package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/golang-jwt/jwt/v5"
)

const testIssuer = "https://issuer.test"

func newTestJWKS(t *testing.T) (*rsa.PrivateKey, *httptest.Server) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	ctx := context.Background()
	jwk, err := jwkset.NewJWKFromKey(key.Public(), jwkset.JWKOptions{
		Metadata: jwkset.JWKMetadataOptions{KID: "test-key", ALG: jwkset.AlgRS256, USE: jwkset.UseSig},
	})
	if err != nil {
		t.Fatalf("build jwk: %v", err)
	}
	store := jwkset.NewMemoryStorage()
	if err := store.KeyWrite(ctx, jwk); err != nil {
		t.Fatalf("store key: %v", err)
	}
	raw, err := store.JSONPublic(ctx)
	if err != nil {
		t.Fatalf("marshal jwks: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}))
	t.Cleanup(srv.Close)
	return key, srv
}

func sign(t *testing.T, key *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test-key"
	s, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func TestVerifierValidToken(t *testing.T) {
	key, srv := newTestJWKS(t)
	v, err := NewVerifier(context.Background(), srv.URL, testIssuer, "")
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	tok := sign(t, key, jwt.MapClaims{
		"sub":   "user-123",
		"email": "a@b.com",
		"iss":   testIssuer,
		"exp":   time.Now().Add(time.Hour).Unix(),
	})

	user, err := v.parse(tok)
	if err != nil {
		t.Fatalf("parse valid token: %v", err)
	}
	if user.ID != "user-123" || user.Email != "a@b.com" {
		t.Errorf("got %+v", user)
	}
}

func TestVerifierRejects(t *testing.T) {
	key, srv := newTestJWKS(t)
	v, err := NewVerifier(context.Background(), srv.URL, testIssuer, "")
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	t.Run("expired", func(t *testing.T) {
		tok := sign(t, key, jwt.MapClaims{"sub": "u", "iss": testIssuer, "exp": time.Now().Add(-time.Hour).Unix()})
		if _, err := v.parse(tok); err == nil {
			t.Error("expected expired token to be rejected")
		}
	})

	t.Run("wrong issuer", func(t *testing.T) {
		tok := sign(t, key, jwt.MapClaims{"sub": "u", "iss": "https://evil.test", "exp": time.Now().Add(time.Hour).Unix()})
		if _, err := v.parse(tok); err == nil {
			t.Error("expected wrong issuer to be rejected")
		}
	})

	t.Run("wrong signing key", func(t *testing.T) {
		other, _ := rsa.GenerateKey(rand.Reader, 2048)
		tok := sign(t, other, jwt.MapClaims{"sub": "u", "iss": testIssuer, "exp": time.Now().Add(time.Hour).Unix()})
		if _, err := v.parse(tok); err == nil {
			t.Error("expected token signed by unknown key to be rejected")
		}
	})

	t.Run("missing sub", func(t *testing.T) {
		tok := sign(t, key, jwt.MapClaims{"iss": testIssuer, "exp": time.Now().Add(time.Hour).Unix()})
		if _, err := v.parse(tok); err == nil {
			t.Error("expected token without sub to be rejected")
		}
	})
}

func TestVerifierAudience(t *testing.T) {
	key, srv := newTestJWKS(t)
	v, err := NewVerifier(context.Background(), srv.URL, testIssuer, "authenticated")
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	claims := func(aud string) jwt.MapClaims {
		return jwt.MapClaims{
			"sub": "u", "iss": testIssuer, "aud": aud,
			"exp": time.Now().Add(time.Hour).Unix(),
		}
	}

	if _, err := v.parse(sign(t, key, claims("authenticated"))); err != nil {
		t.Errorf("expected matching audience to pass: %v", err)
	}
	if _, err := v.parse(sign(t, key, claims("anon"))); err == nil {
		t.Error("expected wrong audience to be rejected")
	}
}

func TestNewVerifierRejectsEmptyJWKS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"keys":[]}`))
	}))
	defer srv.Close()

	if _, err := NewVerifier(context.Background(), srv.URL, testIssuer, ""); err == nil {
		t.Error("expected an issuer with no published keys to fail at startup")
	}
}
