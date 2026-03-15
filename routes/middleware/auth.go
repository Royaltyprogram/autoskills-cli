package middleware

import (
	"strings"
	"time"

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
			if path == "/api/v1/auth/google/start" || path == "/api/v1/auth/google/callback" || path == "/api/v1/auth/cli/refresh" {
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
				if identity, code := store.ValidateAccessTokenWithCode(token); identity != nil && code == 0 {
					if !apiTokenKindAllowedForPath(path, identity.TokenKind) {
						return ecode.Unauthorized(1001, "invalid api token")
					}
					store.MarkAccessTokenSeenAsync(identity.TokenID, time.Now().UTC())
					ctx := service.WithAuthIdentity(c.Request().Context(), *identity)
					c.SetRequest(c.Request().WithContext(ctx))
					return next(c)
				} else if code == service.ErrCodeDeviceAccessTokenExpired {
					return ecode.Unauthorized(service.ErrCodeDeviceAccessTokenExpired, "device access token expired")
				} else if code == service.ErrCodeDeviceRefreshTokenExpired {
					return ecode.Unauthorized(service.ErrCodeDeviceRefreshTokenExpired, "device refresh token expired")
				}
			}

			return ecode.Unauthorized(1001, "invalid api token")
		}
	}
}

func apiTokenKindAllowedForPath(path, kind string) bool {
	switch kind {
	case service.TokenKindCLI, service.TokenKindCLIEnrollment:
		return path == "/api/v1/auth/cli/login"
	case service.TokenKindDeviceRefresh:
		return false
	default:
		return true
	}
}
