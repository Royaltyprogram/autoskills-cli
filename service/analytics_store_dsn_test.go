package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/liushuangls/go-server-template/configs"
)

func TestDefaultAnalyticsStoreDSNUsesSQLiteFallback(t *testing.T) {
	require.Equal(t, "file:agentopt-store?mode=memory&cache=shared&_fk=1", defaultAnalyticsStoreDSN(""))
	require.Equal(t, "data/agentopt-store.db", defaultAnalyticsStoreDSN("data/agentopt-store.json"))
	require.Equal(t, "data/agentopt-store.db", defaultAnalyticsStoreDSN("data/agentopt-store"))
	require.Equal(t, "data/agentopt-local.db?_fk=1", defaultAnalyticsStoreDSN("data/agentopt-local.db?_fk=1"))
}

func TestOpenAnalyticsStoreDBAppliesConfiguredPoolSize(t *testing.T) {
	conf := &configs.Config{}
	conf.DB.Dialect = "sqlite3"
	conf.DB.DSN = filepath.Join(t.TempDir(), "agentopt.db") + "?_fk=1"
	conf.DB.MaxIdle = 3
	conf.DB.MaxActive = 7
	conf.DB.MaxLifetime = 300

	db, err := openAnalyticsStoreDB(conf)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
	})

	require.Equal(t, 7, db.Stats().MaxOpenConnections)
}

func TestAnalyticsStoreOnServerCloseClosesDB(t *testing.T) {
	conf := &configs.Config{}
	conf.DB.Dialect = "sqlite3"
	conf.DB.DSN = filepath.Join(t.TempDir(), "agentopt.db") + "?_fk=1"

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	require.NoError(t, store.OnServerClose(context.Background()))
	require.Error(t, store.Ping(context.Background()))
}
