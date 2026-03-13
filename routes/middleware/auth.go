package middleware

import (
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/Royaltyprogram/aiops/pkg/ecode"
	"github.com/Royaltyprogram/aiops/service"
)

const APIAuthHeader = "X-Crux-Token"

func RequireAPIToken(configuredToken string, staticTokenEnabled bool, store *service.AnalyticsStore) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			path := c.Request().URL.Path
			if !strings.HasPrefix(path, "/api/v1/") {
				return next(c)
			}
			if path == "/api/v1/auth/login" {
				return next(c)
			}

			token := strings.TrimSpace(c.Request().Header.Get(APIAuthHeader))
			if token == "" {
				if cookie, err := c.Cookie(service.WebSessionCookieName); err == nil {
					token = strings.TrimSpace(cookie.Value)
				}
			}
			if token == "" {
				if configuredToken == "" {
					return ecode.Unauthorized(1001, "missing api token")
				}
				return ecode.Unauthorized(1001, "invalid api token")
			}
			if staticTokenEnabled && configuredToken != "" && token == configuredToken {
				ctx := service.WithAuthIdentity(c.Request().Context(), service.AuthIdentity{TokenKind: service.TokenKindStatic})
				c.SetRequest(c.Request().WithContext(ctx))
				return next(c)
			}
			if store != nil {
				if identity, ok := store.ValidateAccessToken(token); ok {
					ctx := service.WithAuthIdentity(c.Request().Context(), *identity)
					c.SetRequest(c.Request().WithContext(ctx))
					return next(c)
				}
			}

			return ecode.Unauthorized(1001, "invalid api token")
		}
	}
}
