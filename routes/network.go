package routes

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/Royaltyprogram/aiops/configs"
)

func configureIPExtractor(e *echo.Echo, conf *configs.Config) error {
	if len(conf.HTTP.TrustedProxyCIDRs) == 0 {
		e.IPExtractor = echo.ExtractIPDirect()
		return nil
	}

	proxyCIDRs, err := parseCIDRs(conf.HTTP.TrustedProxyCIDRs)
	if err != nil {
		return fmt.Errorf("parse trusted proxy cidrs: %w", err)
	}

	options := []echo.TrustOption{
		echo.TrustLoopback(false),
		echo.TrustLinkLocal(false),
		echo.TrustPrivateNet(false),
	}
	for _, cidr := range proxyCIDRs {
		options = append(options, echo.TrustIPRange(cidr))
	}
	e.IPExtractor = echo.ExtractIPFromXFFHeader(options...)
	return nil
}

func newIPAllowlistMiddleware(allowedCIDRs []string) (echo.MiddlewareFunc, error) {
	if len(allowedCIDRs) == 0 {
		return nil, nil
	}

	networks, err := parseCIDRs(allowedCIDRs)
	if err != nil {
		return nil, fmt.Errorf("parse allowed cidrs: %w", err)
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			clientIP := net.ParseIP(c.RealIP())
			if clientIP == nil {
				return echo.NewHTTPError(http.StatusForbidden, "access denied")
			}
			for _, network := range networks {
				if network.Contains(clientIP) {
					return next(c)
				}
			}
			return echo.NewHTTPError(http.StatusForbidden, "access denied")
		}
	}, nil
}

func newAdminIPAllowlistMiddleware(allowedCIDRs []string) (echo.MiddlewareFunc, error) {
	if len(allowedCIDRs) == 0 {
		return nil, nil
	}

	base, err := newIPAllowlistMiddleware(allowedCIDRs)
	if err != nil {
		return nil, fmt.Errorf("parse admin allowed cidrs: %w", err)
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		protected := base(next)
		return func(c *echo.Context) error {
			if !requiresAdminAllowlist(c.Request().URL.Path) {
				return next(c)
			}
			return protected(c)
		}
	}, nil
}

func requiresAdminAllowlist(path string) bool {
	path = strings.TrimSpace(path)
	return path == "/admin" || path == "/api/v1/admin" || strings.HasPrefix(path, "/api/v1/admin/")
}

func parseCIDRs(values []string) ([]*net.IPNet, error) {
	networks := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", value, err)
		}
		networks = append(networks, network)
	}
	return networks, nil
}
