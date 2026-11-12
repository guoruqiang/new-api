package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

func main() {
	if err := cleanupCustomRisk(os.Getenv("SQL_DSN")); err != nil {
		log.Printf("custom risk cleanup failed: %v", err)
		os.Exit(1)
	}
}

// cleanupCustomRisk removes only the retired custom audit's MySQL tables and
// shared task/permission rows. It runs before nw.45 initializes its database.
// SQLite and PostgreSQL deployments retain their normal startup behavior.
func cleanupCustomRisk(dsn string) error {
	if dsn == "" || strings.HasPrefix(dsn, "local") || strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return nil
	}
	config, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		// Parse errors can contain credential text from an invalid DSN.
		return errors.New("SQL_DSN is not a valid MySQL DSN")
	}
	if config.DBName == "" {
		return errors.New("SQL_DSN must select a database for custom risk cleanup")
	}
	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second
	}
	if config.ReadTimeout == 0 {
		config.ReadTimeout = 30 * time.Second
	}
	if config.WriteTimeout == 0 {
		config.WriteTimeout = 30 * time.Second
	}
	db, err := gorm.Open(mysql.New(mysql.Config{DSN: config.FormatDSN(), SkipInitializeWithVersion: true}), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return fmt.Errorf("connect to MySQL: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db = db.WithContext(ctx)

	riskTables := []string{
		"risk_violations", "risk_user_scores", "risk_events", "risk_counters",
		"risk_group_namespaces", "risk_providers", "risk_policies", "risk_configs", "risk_states",
		"risk_refusals", "risk_subject_blocks", "risk_reviews", "risk_signals", "risk_reputation_entries",
	}
	tablesToInspect := append([]string{"system_tasks", "system_task_locks", "casbin_rule"}, riskTables...)
	var existingTables []string
	if err := db.Table("information_schema.tables").Where("table_schema = ? AND table_name IN ?", config.DBName, tablesToInspect).
		Pluck("table_name", &existingTables).Error; err != nil {
		return fmt.Errorf("inspect custom risk tables: %w", err)
	}
	existing := make(map[string]bool, len(existingTables))
	for _, table := range existingTables {
		existing[table] = true
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		for _, table := range []string{"system_task_locks", "system_tasks"} {
			if existing[table] {
				if err := tx.Table(table).Where("type = ?", "risk_cleanup").Delete(&struct{}{}).Error; err != nil {
					return err
				}
			}
		}
		if existing["casbin_rule"] {
			return tx.Table("casbin_rule").Where("ptype = ? AND v1 = ?", "p", "risk").Delete(&struct{}{}).Error
		}
		return nil
	}); err != nil {
		return fmt.Errorf("remove custom risk tasks and permissions: %w", err)
	}
	removed := 0
	for _, table := range riskTables {
		if !existing[table] {
			continue
		}
		// MySQL's GORM DropTable disables foreign-key checks. Keep them enabled
		// here so an unexpected external reference stops this temporary cleanup.
		// IF EXISTS also makes concurrent container starts safe to retry.
		if err := db.Exec("DROP TABLE IF EXISTS ?", clause.Table{Name: table}).Error; err != nil {
			return fmt.Errorf("drop %s: %w", table, err)
		}
		removed++
	}
	log.Printf("custom risk cleanup complete: removed %d tables", removed)
	return nil
}
