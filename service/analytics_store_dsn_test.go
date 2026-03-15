package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Royaltyprogram/aiops/configs"
)

func TestDefaultAnalyticsStoreDSNUsesSQLiteFallback(t *testing.T) {
	require.Equal(t, "file:crux-store?mode=memory&cache=shared&_fk=1", defaultAnalyticsStoreDSN(""))
	require.Equal(t, "data/crux-store.db", defaultAnalyticsStoreDSN("data/crux-store.json"))
	require.Equal(t, "data/crux-store.db", defaultAnalyticsStoreDSN("data/crux-store"))
	require.Equal(t, "data/crux-local.db?_fk=1", defaultAnalyticsStoreDSN("data/crux-local.db?_fk=1"))
}

func TestOpenAnalyticsStoreDBAppliesConfiguredPoolSize(t *testing.T) {
	conf := &configs.Config{}
	conf.DB.Dialect = "sqlite3"
	conf.DB.DSN = filepath.Join(t.TempDir(), "crux.db") + "?_fk=1"
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
	conf.DB.DSN = filepath.Join(t.TempDir(), "crux.db") + "?_fk=1"

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	require.NoError(t, store.OnServerClose(context.Background()))
	require.Error(t, store.Ping(context.Background()))
}

func TestAnalyticsStoreDDLUsesMySQLCompatibleKeyTypes(t *testing.T) {
	metaDDL := analyticsStoreMetaTableDDL("mysql")
	recordDDL := analyticsStoreRecordTableDDL("mysql")

	require.Contains(t, metaDDL, "meta_key VARCHAR(191) PRIMARY KEY")
	require.Contains(t, metaDDL, "meta_value LONGTEXT NOT NULL")
	require.Contains(t, recordDDL, "record_type VARCHAR(191) NOT NULL")
	require.Contains(t, recordDDL, "scope_id VARCHAR(191) NOT NULL")
	require.Contains(t, recordDDL, "record_id VARCHAR(191) NOT NULL")
	require.Contains(t, recordDDL, "payload LONGTEXT NOT NULL")
}
