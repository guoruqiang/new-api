package model

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

func auditLogIndexColumns(t *testing.T, db *gorm.DB, indexes []gorm.Index) map[string][]string {
	t.Helper()
	columnsByName := make(map[string][]string, len(indexes))
	for _, index := range indexes {
		columns := index.Columns()
		if db.Dialector.Name() == "postgres" {
			// The PostgreSQL driver's GetIndexes joins attributes with ANY(indkey)
			// without ordering them. Read each key's definition in index order.
			columns = nil
			require.NoError(t, db.Raw(`
SELECT pg_get_indexdef(i.indexrelid, key_position.position, true)
FROM pg_index i
JOIN pg_class index_relation ON index_relation.oid = i.indexrelid
JOIN pg_namespace index_schema ON index_schema.oid = index_relation.relnamespace
CROSS JOIN LATERAL generate_series(1, i.indnatts) AS key_position(position)
WHERE index_schema.nspname = CURRENT_SCHEMA() AND index_relation.relname = ?
ORDER BY key_position.position`, index.Name()).Scan(&columns).Error)
			require.NotEmpty(t, columns, "index %s must have ordered keys", index.Name())
		}
		columnsByName[index.Name()] = columns
	}
	return columnsByName
}

// TestAuditLogSortIndexDatabaseMatrix verifies audit ordering and index migrations across supported databases.
func TestAuditLogSortIndexDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "audit_sort_test_"}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			previousType := common.LogDatabaseType()
			common.SetLogDatabaseType(common.DatabaseType(dialect))
			t.Cleanup(func() {
				common.SetLogDatabaseType(previousType)
				assert.NoError(t, sqlDB.Close())
			})
			versionSQL := "SELECT version()"
			if dialect == "sqlite" {
				versionSQL = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
			t.Logf("database version: %s", version)
			for _, upgrade := range []bool{false, true} {
				t.Run(fmt.Sprintf("upgrade=%v", upgrade), func(t *testing.T) {
					db := db
					if dialect == "postgres" {
						db = db.Begin()
						require.NoError(t, db.Error)
						t.Cleanup(func() { assert.NoError(t, db.Rollback().Error) })
						schemaName := fmt.Sprintf("audit_sort_test_%d", time.Now().UnixNano())
						require.NoError(t, db.Exec("CREATE SCHEMA ?", clause.Table{Name: schemaName}).Error)
						require.NoError(t, db.Exec("SET LOCAL search_path TO ?", clause.Table{Name: schemaName}).Error)
					} else {
						t.Cleanup(func() { assert.NoError(t, db.Migrator().DropTable(&AuditLog{})) })
					}
					previousLogDB := LOG_DB
					LOG_DB = db
					t.Cleanup(func() { LOG_DB = previousLogDB })
					require.NoError(t, MigrateAuditLogs())
					if upgrade {
						require.NoError(t, db.Migrator().DropIndex(&AuditLog{}, "idx_audit_time_event"))
					}
					entries := []AuditLog{
						{EventId: "event-older", CreatedAt: 100, UserId: 1, ActorRole: common.RoleCommonUser},
						{EventId: "event-a", CreatedAt: 200, UserId: 1, ActorRole: common.RoleCommonUser},
						{EventId: "event-z", CreatedAt: 200, UserId: 1, ActorRole: common.RoleCommonUser},
					}
					require.NoError(t, db.Create(&entries).Error)
					originalIndexes, err := db.Migrator().GetIndexes(&AuditLog{})
					require.NoError(t, err)
					originalColumns := auditLogIndexColumns(t, db, originalIndexes)
					for range 2 {
						require.NoError(t, MigrateAuditLogs())
					}
					indexes, err := db.Migrator().GetIndexes(&AuditLog{})
					require.NoError(t, err)
					columns := auditLogIndexColumns(t, db, indexes)
					byName := make(map[string]gorm.Index, len(indexes))
					for _, index := range indexes {
						byName[index.Name()] = index
					}
					index, ok := byName["idx_audit_time_event"]
					require.True(t, ok, "migration must create the audit ordering index")
					t.Logf("audit ordering index columns: %v", columns[index.Name()])
					assert.Equal(t, []string{"created_at", "event_id"}, columns[index.Name()])
					for _, original := range originalIndexes {
						preserved, ok := byName[original.Name()]
						require.True(t, ok, "existing index %s must remain", original.Name())
						assert.Equal(t, originalColumns[original.Name()], columns[preserved.Name()], original.Name())
						originalUnique, originalKnown := original.Unique()
						preservedUnique, preservedKnown := preserved.Unique()
						assert.Equal(t, originalKnown, preservedKnown, original.Name())
						assert.Equal(t, originalUnique, preservedUnique, original.Name())
					}
					var stored []AuditLog
					require.NoError(t, db.Order("id").Find(&stored).Error)
					assert.Equal(t, entries, stored, "migration must preserve existing audit records")
					logs, total, err := GetAuditLogs(AuditLogFilter{}, 0, 2, common.RoleRootUser)
					require.NoError(t, err)
					assert.EqualValues(t, 3, total)
					require.Len(t, logs, 2)
					assert.Equal(t, []string{"event-z", "event-a"}, []string{logs[0].EventId, logs[1].EventId})
					logs, total, err = GetAuditLogs(AuditLogFilter{}, 2, 2, common.RoleRootUser)
					require.NoError(t, err)
					assert.EqualValues(t, 3, total)
					require.Len(t, logs, 1)
					assert.Equal(t, "event-older", logs[0].EventId)
					assert.Error(t, db.Create(&AuditLog{EventId: "event-a"}).Error, "event IDs must remain unique")
				})
			}
		})
	}
}
