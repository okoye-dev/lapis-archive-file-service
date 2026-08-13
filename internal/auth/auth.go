package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/okoye-dev/lapis-archive-file-service/internal/domain"
	"github.com/okoye-dev/lapis-archive-file-service/internal/transport/rest"
)

const contextUserKey = "authUser"

type Verifier struct {
	keyfunc  jwt.Keyfunc
	issuer   string
	audience string
}

func NewVerifier(ctx context.Context, jwksURL, issuer, audience string) (*Verifier, error) {
	if jwksURL == "" {
		return nil, errors.New("jwks url is empty")
	}

	if err := checkJWKSHasKeys(ctx, jwksURL); err != nil {
		return nil, err
	}

	k, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("loading jwks: %w", err)
	}

	return &Verifier{keyfunc: k.Keyfunc, issuer: issuer, audience: audience}, nil
}

// An issuer still signing with a shared secret serves an empty key set, which
// would otherwise fail confusingly on the first request instead of at boot.
func checkJWKSHasKeys(ctx context.Context, jwksURL string) error {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return fmt.Errorf("building jwks request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching jwks: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching jwks: unexpected status %d", resp.StatusCode)
	}

	var set struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return fmt.Errorf("decoding jwks: %w", err)
	}
	if len(set.Keys) == 0 {
		return fmt.Errorf("jwks at %s has no keys: the issuer is still signing with a shared secret, switch it to asymmetric signing keys (ES256/RS256)", jwksURL)
	}
	return nil
}

func (v *Verifier) parse(tokenString string) (*domain.User, error) {
	opts := []jwt.ParserOption{jwt.WithValidMethods([]string{"RS256", "ES256"})}
	if v.issuer != "" {
		opts = append(opts, jwt.WithIssuer(v.issuer))
	}
	if v.audience != "" {
		opts = append(opts, jwt.WithAudience(v.audience))
	}

	token, err := jwt.Parse(tokenString, v.keyfunc, opts...)
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("unexpected claims type")
	}

	sub, _ := claims["sub"].(string)
	if sub == "" {
		return nil, errors.New("token missing sub")
	}
	email, _ := claims["email"].(string)

	return &domain.User{ID: sub, Email: email}, nil
}

func bearerToken(c *gin.Context) string {
	header := c.GetHeader("Authorization")
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func (v *Verifier) Required() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerToken(c)
		if token == "" {
			rest.Error(c, 401, "Authentication required")
			c.Abort()
			return
		}
		user, err := v.parse(token)
		if err != nil {
			rest.Error(c, 401, "Invalid or expired token")
			c.Abort()
			return
		}
		c.Set(contextUserKey, user)
		c.Next()
	}
}

func (v *Verifier) Optional() gin.HandlerFunc {
	return func(c *gin.Context) {
		if token := bearerToken(c); token != "" {
			if user, err := v.parse(token); err == nil {
				c.Set(contextUserKey, user)
			}
		}
		c.Next()
	}
}

func SetUser(c *gin.Context, u *domain.User) {
	c.Set(contextUserKey, u)
}

func UserFrom(c *gin.Context) (*domain.User, bool) {
	value, ok := c.Get(contextUserKey)
	if !ok {
		return nil, false
	}
	user, ok := value.(*domain.User)
	return user, ok
}
