package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/okoye-dev/lapis-archive-file-service/internal/domain"
	"github.com/okoye-dev/lapis-archive-file-service/internal/transport/rest"
)

const contextUserKey = "authUser"

type Verifier struct {
	keyfunc jwt.Keyfunc
	issuer  string
}

func NewVerifier(ctx context.Context, jwksURL, issuer string) (*Verifier, error) {
	if jwksURL == "" {
		return nil, errors.New("jwks url is empty")
	}

	k, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("loading jwks: %w", err)
	}

	return &Verifier{keyfunc: k.Keyfunc, issuer: issuer}, nil
}

func (v *Verifier) parse(tokenString string) (*domain.User, error) {
	opts := []jwt.ParserOption{jwt.WithValidMethods([]string{"RS256", "ES256"})}
	if v.issuer != "" {
		opts = append(opts, jwt.WithIssuer(v.issuer))
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
