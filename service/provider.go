package service

import (
	"github.com/google/wire"

	"github.com/liushuangls/go-server-template/configs"
)

var ProviderSet = wire.NewSet(
	wire.Struct(new(Options), "*"),
	NewAnalyticsStore,
	NewAnalyticsService,
	NewHealthService,
)

type Options struct {
	Config         *configs.Config
	AnalyticsStore *AnalyticsStore
}
