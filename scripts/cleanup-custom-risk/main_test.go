package main

import (
	"os"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCleanupCustomRiskSkipsOtherDatabaseTypes(t *testing.T) {
	for _, dsn := range []string{"", "local", "local.db", "postgres://unreachable/database", "postgresql://unreachable/database"} {
		t.Run(dsn, func(t *testing.T) {
			require.NoError(t, cleanupCustomRisk(dsn))
		})
	}
}

func TestCleanupCustomRiskRejectsInvalidMySQLTarget(t *testing.T) {
	for _, dsn := range []string{"invalid-secret-dsn", "user:secret@tcp(localhost:3306)/"} {
		err := cleanupCustomRisk(dsn)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "secret")
	}
}

// All tables are created only in the explicitly named disposable test database.
// Values on shared/business tables protect data and status preservation during
// both the initial downgrade cleanup and a repeated container start.
func TestCleanupCustomRiskMySQL(t *testing.T) {
	dsn := os.Getenv("RISK_CLEANUP_TEST_DSN")
	if dsn == "" {
		t.Skip("RISK_CLEANUP_TEST_DSN is required for real MySQL verification")
	}
	config, err := mysqldriver.ParseDSN(dsn)
	require.NoError(t, err)
	require.Equal(t, "newapi_risk_cleanup_test", config.DBName, "use a disposable test database")
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, sqlDB.Close()) })
	var version string
	require.NoError(t, db.Raw("SELECT VERSION()").Scan(&version).Error)
	t.Logf("database: MySQL %s", version)

	riskTables := []string{
		"risk_states", "risk_configs", "risk_policies", "risk_providers", "risk_group_namespaces",
		"risk_events", "risk_counters", "risk_violations", "risk_user_scores",
		"risk_refusals", "risk_subject_blocks", "risk_reviews", "risk_signals", "risk_reputation_entries",
	}
	preservedTables := []string{"users", "tokens", "channels", "options", "quota_data", "audit_logs", "risk_unrelated"}
	sharedTables := []string{"system_tasks", "system_task_locks", "casbin_rule"}
	allTables := append(append(append([]string{}, riskTables...), preservedTables...), sharedTables...)
	for _, table := range allTables {
		require.NoError(t, db.Migrator().DropTable(table))
	}
	t.Cleanup(func() {
		assert.NoError(t, db.Exec("DROP TABLE IF EXISTS retained_reference").Error)
		for _, table := range allTables {
			assert.NoError(t, db.Migrator().DropTable(table))
		}
	})

	// A new, empty installation has no legacy objects and still starts cleanly.
	require.NoError(t, cleanupCustomRisk(dsn))
	require.NoError(t, cleanupCustomRisk(dsn))
	type fixtureRow struct {
		ID     int64
		Type   string
		Ptype  string
		V1     string
		Value  string
		Status int
		Quota  int64
	}
	for _, table := range allTables {
		require.NoError(t, db.Table(table).AutoMigrate(&fixtureRow{}))
		require.NoError(t, db.Table(table).Create(&fixtureRow{ID: 1, Type: "keep", Ptype: "p", V1: "users", Value: "preserved", Status: 2, Quota: 12345}).Error)
	}
	for _, table := range []string{"system_tasks", "system_task_locks"} {
		require.NoError(t, db.Table(table).Create(&fixtureRow{ID: 2, Type: "risk_cleanup"}).Error)
	}
	require.NoError(t, db.Table("casbin_rule").Create(&fixtureRow{ID: 2, Ptype: "p", V1: "risk"}).Error)
	require.NoError(t, db.Table("casbin_rule").Create(&fixtureRow{ID: 3, Ptype: "g", V1: "risk"}).Error)

	// Downgrade a database with risk data, then run the next startup again.
	for range 2 {
		require.NoError(t, cleanupCustomRisk(dsn))
		for _, table := range riskTables {
			assert.False(t, db.Migrator().HasTable(table), table)
		}
		for _, table := range preservedTables {
			var row fixtureRow
			require.NoError(t, db.Table(table).First(&row).Error)
			assert.Equal(t, fixtureRow{ID: 1, Type: "keep", Ptype: "p", V1: "users", Value: "preserved", Status: 2, Quota: 12345}, row, table)
		}
		for _, table := range []string{"system_tasks", "system_task_locks"} {
			var ids []int64
			require.NoError(t, db.Table(table).Pluck("id", &ids).Error)
			assert.Equal(t, []int64{1}, ids, table)
		}
		var policyIDs []int64
		require.NoError(t, db.Table("casbin_rule").Order("id").Pluck("id", &policyIDs).Error)
		assert.Equal(t, []int64{1, 3}, policyIDs)
	}

	// An unexpected external foreign key must fail cleanup, preserving that
	// reference and its target instead of disabling integrity checks.
	require.NoError(t, db.Table("risk_states").AutoMigrate(&fixtureRow{}))
	require.NoError(t, db.Exec("CREATE TABLE retained_reference (id BIGINT PRIMARY KEY, risk_id BIGINT, FOREIGN KEY (risk_id) REFERENCES risk_states(id))").Error)
	err = cleanupCustomRisk(dsn)
	require.ErrorContains(t, err, "drop risk_states")
	assert.True(t, db.Migrator().HasTable("risk_states"))
	assert.True(t, db.Migrator().HasTable("retained_reference"))
}
