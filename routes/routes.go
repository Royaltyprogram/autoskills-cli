package routes

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/go-redis/redis_rate/v10"
	"github.com/labstack/echo/v5"
	echoMiddleware "github.com/labstack/echo/v5/middleware"
	"golang.org/x/sync/errgroup"

	"github.com/liushuangls/go-server-template/configs"
	"github.com/liushuangls/go-server-template/routes/common"
	"github.com/liushuangls/go-server-template/routes/middleware"
	"github.com/liushuangls/go-server-template/service"
)

func NewEcho(conf *configs.Config, logger *slog.Logger, store *service.AnalyticsStore) (*echo.Echo, error) {
	e := echo.New()

	e.Logger = logger
	e.HTTPErrorHandler = common.EchoErrorHandler
	if err := configureIPExtractor(e, conf); err != nil {
		return nil, err
	}

	cb, err := common.NewCustomBinder()
	if err != nil {
		return nil, err
	}
	e.Binder = cb

	middlewareChain := []echo.MiddlewareFunc{
		echoMiddleware.Recover(),
		echoMiddleware.RequestLogger(),
	}
	ipAllowlist, err := newIPAllowlistMiddleware(conf.HTTP.AllowedCIDRs)
	if err != nil {
		return nil, err
	}
	if ipAllowlist != nil {
		middlewareChain = append(middlewareChain, ipAllowlist)
	}
	if len(conf.HTTP.AllowedOrigins) > 0 {
		middlewareChain = append(middlewareChain, echoMiddleware.CORSWithConfig(echoMiddleware.CORSConfig{
			AllowOrigins:     conf.HTTP.AllowedOrigins,
			AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodOptions},
			AllowHeaders:     []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAccept, middleware.APIAuthHeader},
			AllowCredentials: true,
			MaxAge:           int((12 * time.Hour).Seconds()),
		}))
	}
	if conf.HTTP.RateLimitPerMinute > 0 {
		ratePerSecond := float64(conf.HTTP.RateLimitPerMinute) / 60.0
		burst := conf.HTTP.RateLimitPerMinute
		if burst < 1 {
			burst = 1
		}
		middlewareChain = append(middlewareChain, echoMiddleware.RateLimiterWithConfig(echoMiddleware.RateLimiterConfig{
			Store: echoMiddleware.NewRateLimiterMemoryStoreWithConfig(echoMiddleware.RateLimiterMemoryStoreConfig{
				Rate:      ratePerSecond,
				Burst:     burst,
				ExpiresIn: 5 * time.Minute,
			}),
			Skipper: func(c *echo.Context) bool {
				path := c.Request().URL.Path
				if path == "/health" || path == "/healthz" || path == "/readyz" {
					return true
				}
				return !strings.HasPrefix(path, "/api/")
			},
		}))
	}
	middlewareChain = append(middlewareChain, middleware.RequireAPIToken(conf.App.APIToken, conf.AllowsStaticToken(), store))
	e.Use(middlewareChain...)

	return e, nil
}

type HttpEngine struct {
	Options
}

type Registrable interface {
	RegisterRoute(group *echo.Group)
}

func NewHttpEngine(opt Options) *HttpEngine {
	return &HttpEngine{opt}
}

func (h *HttpEngine) RegisterRoute() {
	g := h.Router.Group("")
	if h.Limiter != nil {
		g.Use(
			middleware.RateLimitWithIP(h.Limiter, redis_rate.PerMinute(60), "total"),
		)
	}

	v := reflect.ValueOf(h.Options)
	for i := 0; i < v.NumField(); i++ {
		if router, ok := v.Field(i).Interface().(Registrable); ok {
			router.RegisterRoute(g)
		}
	}

	printRoutes(h.Router)
}

func printRoutes(e *echo.Echo) {
	fmt.Println("==== Registered Routes ====")
	for _, r := range e.Router().Routes() {
		if r.Path == "/" || r.Path == "/*" {
			continue
		}
		// r.Name 是 handler 的函数名，视情况打印
		fmt.Printf("%-6s %-30s -> %s\n", r.Method, r.Path, r.Name)
	}
	fmt.Println("===========================")
}

func (h *HttpEngine) Run(g *errgroup.Group) (*http.Server, error) {
	h.RegisterRoute()

	srv := &http.Server{
		Addr:    h.Conf.App.Addr,
		Handler: h.Router,
	}

	g.Go(func() error {
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	})

	return srv, nil
}
