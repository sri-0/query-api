// Package middleware holds Echo middleware for auth forwarding and request logging.
package middleware

import (
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"

	"github.com/prismgroup/query-api/internal/osclient"
)

const (
	ctxUser  = "user"
	ctxRoles = "roles"
)

// Auth copies the Authorization header into the request context so it is
// forwarded to OpenSearch, and extracts JWT claims (unverified) for audit.
// Nothing is enforced here; OpenSearch decides whether the token is valid.
func Auth() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			h := c.Request().Header.Get("Authorization")
			user, roles := "anonymous", []string(nil)
			if h != "" {
				ctx := osclient.WithAuthorization(c.Request().Context(), h)
				c.SetRequest(c.Request().WithContext(ctx))
				if tok, ok := strings.CutPrefix(h, "Bearer "); ok {
					user, roles = claims(tok)
				}
			}
			c.Set(ctxUser, user)
			c.Set(ctxRoles, roles)
			return next(c)
		}
	}
}

// User returns the caller identity recorded by Auth.
func User(c echo.Context) string {
	if u, ok := c.Get(ctxUser).(string); ok {
		return u
	}
	return "anonymous"
}

func claims(tok string) (string, []string) {
	parser := jwt.NewParser()
	var mc jwt.MapClaims
	if _, _, err := parser.ParseUnverified(tok, &mc); err != nil {
		return "unknown", nil
	}
	user := "unknown"
	for _, k := range []string{"preferred_username", "email", "sub"} {
		if s, ok := mc[k].(string); ok && s != "" {
			user = s
			break
		}
	}
	var roles []string
	if rs, ok := mc["roles"].([]any); ok {
		for _, r := range rs {
			if s, ok := r.(string); ok {
				roles = append(roles, s)
			}
		}
	}
	return user, roles
}
