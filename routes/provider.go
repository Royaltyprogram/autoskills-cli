package routes

import (
	"github.com/bsm/redislock"
	"github.com/go-redis/redis_rate/v10"
	"github.com/google/wire"
	"github.com/labstack/echo/v5"

	"github.com/liushuangls/go-server-template/configs"
	"github.com/liushuangls/go-server-template/routes/controller"
)

var ProviderSet = wire.NewSet(
	wire.Struct(new(Options), "*"),
	wire.Struct(new(controller.Options), "*"),
	NewEcho,
	NewNoOpLimiter,
	NewNoOpLocker,
	NewHttpEngine,
	controller.NewHealthRoute,
	controller.NewAnalyticsRoute,
	controller.NewDashboardRoute,
)

type Options struct {
	Router  *echo.Echo
	Conf    *configs.Config
	Limiter *redis_rate.Limiter

	Health    *controller.HealthRoute
	Analytics *controller.AnalyticsRoute
	Dashboard *controller.DashboardRoute
}

func NewNoOpLimiter() *redis_rate.Limiter {
	return nil
}

func NewNoOpLocker() *redislock.Client {
	return nil
}
