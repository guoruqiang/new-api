package model

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func useRiskDB(t *testing.T, initialModels ...[]any) *gorm.DB {
	t.Helper()
	previousDB, previousType := DB, common.MainDatabaseType()
	databaseType := common.DatabaseTypeSQLite
	var dialector gorm.Dialector = sqlite.Open(filepath.Join(t.TempDir(), "risk.db") + "?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	config := newGormConfig(true)
	config.Logger = logger.Default.LogMode(logger.Silent)
	prefix := ""
	postgresSchema := ""
	mysqlDSN, postgresDSN := os.Getenv("RISK_MYSQL_TEST_DSN"), os.Getenv("RISK_POSTGRES_TEST_DSN")
	require.False(t, mysqlDSN != "" && postgresDSN != "", "select only one external test database")
	if mysqlDSN != "" || postgresDSN != "" {
		prefix = "risk_test_" + strings.ToLower(rand.Text()) + "_"
	}
	if mysqlDSN != "" {
		databaseType = common.DatabaseTypeMySQL
		dialector = mysqlMigrationDialector{mysql.Dialector{Config: &mysql.Config{DSN: mysqlDSN}}}
	} else if postgresDSN != "" {
		databaseType = common.DatabaseTypePostgreSQL
		parsedDSN, err := url.Parse(postgresDSN)
		require.NoError(t, err)
		require.Contains(t, []string{"postgres", "postgresql"}, parsedDSN.Scheme, "PostgreSQL test DSN must be a connection URL")
		// Explicit index names are schema-wide in PostgreSQL. Isolate the schema
		// as well as table names so another test's indexes cannot be reused.
		postgresSchema = strings.TrimSuffix(prefix, "_")
		query := parsedDSN.Query()
		query.Set("search_path", postgresSchema)
		parsedDSN.RawQuery = query.Encode()
		dialector = postgresMigrationDialector{postgres.Dialector{Config: &postgres.Config{
			DSN: parsedDSN.String(), PreferSimpleProtocol: true,
		}}}
		config = newGormConfig(false)
		config.Logger = logger.Default.LogMode(logger.Silent)
	}
	if databaseType != common.DatabaseTypeSQLite {
		config.NamingStrategy = schema.NamingStrategy{TablePrefix: prefix}
	}
	db, err := gorm.Open(dialector, config)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	DB = db
	common.SetMainDatabaseType(databaseType)
	initCol()
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		initCol()
		require.NoError(t, sqlDB.Close())
	})
	if postgresSchema != "" {
		// The schema name contains only our generated letters, digits and
		// underscores. CREATE must fail if it unexpectedly already exists.
		require.NoError(t, db.Exec("CREATE SCHEMA "+postgresSchema).Error)
		t.Cleanup(func() {
			// Table cleanup runs first. No CASCADE: never remove unowned objects.
			assert.NoError(t, db.Exec("DROP SCHEMA "+postgresSchema).Error)
		})
	}
	models := append(RiskModels(), &Option{})
	if databaseType != common.DatabaseTypeSQLite {
		before, err := db.Migrator().GetTables()
		require.NoError(t, err)
		ownedTables := make([]string, 0, len(models))
		for _, row := range models {
			statement := &gorm.Statement{DB: db}
			require.NoError(t, statement.Parse(row))
			table := statement.Schema.Table
			require.True(t, strings.HasPrefix(table, prefix), "refuse to use an unprefixed test table")
			require.NotContains(t, before, table, "refuse to migrate or clean up an existing table")
			ownedTables = append(ownedTables, table)
		}
		// Every name was absent before this fixture. Cleanup uses this exact list,
		// never a wildcard, so other test runs and existing business tables survive.
		t.Cleanup(func() {
			for _, table := range ownedTables {
				assert.NoError(t, db.Migrator().DropTable(table), table)
			}
			after, err := db.Migrator().GetTables()
			if assert.NoError(t, err) {
				for _, table := range ownedTables {
					assert.NotContains(t, after, table, "temporary table must be removed")
				}
				for _, table := range before {
					assert.Contains(t, after, table, "existing table must remain")
				}
			}
		})
		var version string
		require.NoError(t, db.Raw("SELECT VERSION()").Scan(&version).Error)
		t.Logf("%s %s; isolated table prefix=%s", databaseType, version, prefix)
	}
	migrate := models
	if len(initialModels) > 0 {
		migrate = append(initialModels[0], &Option{})
	}
	require.NoError(t, db.AutoMigrate(migrate...))
	return db
}

func TestRiskNamespaceLifecycleAndOptionHooks(t *testing.T) {
	db := useRiskDB(t)
	oldRatios, oldUsable := ratio_setting.GroupRatio2JSONString(), setting.UserUsableGroups2JSONString()
	common.OptionMapRWMutex.Lock()
	oldOptions := common.OptionMap
	common.OptionMap = maps.Clone(oldOptions)
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldRatios))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldUsable))
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptions
		common.OptionMapRWMutex.Unlock()
	})

	require.NoError(t, UpdateOption("GroupRatio", `{"vip":1,"auto":1}`))
	rows, err := ListRiskGroupNamespaces(false)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	original := rows[0]
	assert.Equal(t, "vip", original.GroupName)
	assert.Equal(t, 1, original.Generation)
	assert.Len(t, original.Namespace, 32)
	revision, err := GetRiskChangeRevision()
	require.NoError(t, err)
	require.NoError(t, ReconcileRiskGroupsFromOption("GroupRatio"))
	after, err := GetRiskChangeRevision()
	require.NoError(t, err)
	assert.Equal(t, revision, after, "unchanged directory must not invalidate snapshots")

	require.NoError(t, UpdateOption("UserUsableGroups", `{"other":"Another group"}`))
	after, err = GetRiskChangeRevision()
	require.NoError(t, err)
	assert.Equal(t, revision, after, "usable descriptions do not define namespace membership")
	require.NoError(t, UpdateOptionsBulk(map[string]string{"group_ratio_setting.group_ratio": `{}`}))
	rows, err = ListRiskGroupNamespaces(false)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.False(t, rows[0].Active)
	assert.Positive(t, rows[0].RetiredAt)
	require.NoError(t, UpdateOption("group_ratio_setting.group_ratio", `{"vip":2}`))
	active, err := ListRiskGroupNamespaces(true)
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, 2, active[0].Generation)
	assert.NotEqual(t, original.Namespace, active[0].Namespace)
	rows, err = ListRiskGroupNamespaces(false)
	require.NoError(t, err)
	assert.Len(t, rows, 2, "retired generation remains traceable")

	// The saved key, not a stale memory copy, determines post-save reconciliation.
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"stale":1}`))
	require.NoError(t, ReconcileRiskGroupsFromOption("group_ratio_setting.group_ratio"))
	active, err = ListRiskGroupNamespaces(true)
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, "vip", active[0].GroupName)

	// v1.3 deliberately keeps a successful settings save when reconciliation fails.
	require.NoError(t, db.Migrator().DropTable(&RiskGroupNamespace{}))
	require.NoError(t, UpdateOption("GroupRatio", `{"saved":1}`))
	var option Option
	require.NoError(t, db.Where(&Option{Key: "GroupRatio"}).First(&option).Error)
	assert.JSONEq(t, `{"saved":1}`, option.Value)
	assert.True(t, ratio_setting.ContainsGroupRatio("saved"))
}

func TestRiskConcurrentInitializationAndPublish(t *testing.T) {
	db := useRiskDB(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() { <-start; results <- ReconcileRiskGroupNamespaces([]string{"vip", "auto"}) })
	}
	close(start)
	workers.Wait()
	for range 2 {
		require.NoError(t, <-results)
	}
	var count int64
	require.NoError(t, db.Model(&RiskState{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	rows, err := ListRiskGroupNamespaces(true)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 1, rows[0].Generation)

	start = make(chan struct{})
	for range 2 {
		workers.Go(func() { <-start; _, err := PublishRiskConfig(0, `{"enabled":false}`, 7); results <- err })
	}
	close(start)
	workers.Wait()
	var published, conflicted int
	for range 2 {
		err := <-results
		if err == nil {
			published++
		} else {
			require.ErrorIs(t, err, ErrRiskRevisionConflict)
			conflicted++
		}
	}
	assert.Equal(t, 1, published)
	assert.Equal(t, 1, conflicted)
	current, err := GetLatestRiskConfig()
	require.NoError(t, err)
	assert.EqualValues(t, 1, current.Revision)
	revision, err := GetRiskChangeRevision()
	require.NoError(t, err)
	assert.EqualValues(t, 2, revision, "one directory change plus one successful publish")

	policy, err := CreateRiskPolicyRevision("baseline", "Baseline", 0, `{}`, "hash-v1", 7)
	require.NoError(t, err)
	_, err = CreateRiskPolicyRevision("baseline", "Baseline", 0, `{}`, "stale", 8)
	require.ErrorIs(t, err, ErrRiskRevisionConflict)
	second, err := CreateRiskPolicyRevision("baseline", "Baseline", 1, `{"skip_gpt_models":true}`, "hash-v2", 7)
	require.NoError(t, err)
	assert.Equal(t, 2, second.Revision)
	preserved, err := GetRiskPolicy("baseline", 1)
	require.NoError(t, err)
	assert.Equal(t, policy.Payload, preserved.Payload)
}

func TestRiskPublishAndProviderRollback(t *testing.T) {
	db := useRiskDB(t)
	require.NoError(t, ReconcileRiskGroupNamespaces(nil))
	rejected := errors.New("control row write rejected")
	stateStatement := &gorm.Statement{DB: db}
	require.NoError(t, stateStatement.Parse(&RiskState{}))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("risk_test:reject_state", func(tx *gorm.DB) {
		if tx.Statement.Table == stateStatement.Schema.Table {
			tx.AddError(rejected)
		}
	}))
	_, err := PublishRiskConfig(0, `{}`, 1)
	require.ErrorIs(t, err, rejected)
	latest, err := GetLatestRiskConfig()
	require.NoError(t, err)
	assert.Nil(t, latest, "configuration and revision must roll back together")
	provider := RiskProvider{Name: "local", ProviderType: "openai", BaseURL: "http://127.0.0.1", Model: "omni-moderation-latest"}
	require.ErrorIs(t, SaveRiskProvider(&provider, []string{"test-key"}), rejected)
	assert.Zero(t, provider.Id, "failed insert must not leak a rolled-back id to a caller retry")
	var count int64
	require.NoError(t, db.Model(&RiskProvider{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, db.Callback().Update().Remove("risk_test:reject_state"))

	require.NoError(t, SaveRiskProvider(&provider, []string{"test-key"}))
	assert.Positive(t, provider.Id)
	assert.Equal(t, 1, provider.KeysRevision)
	provider.BaseURL = "http://localhost"
	require.NoError(t, SaveRiskProvider(&provider, nil))
	assert.Equal(t, "test-key", provider.ApiKeys)
	assert.Equal(t, 1, provider.KeysRevision)
	require.NoError(t, SaveRiskProvider(&provider, []string{}))
	stored, err := GetRiskProvider(provider.Id)
	require.NoError(t, err)
	assert.Equal(t, "openai", stored.ProviderType)
	assert.Equal(t, "omni-moderation-latest", stored.Model)
	assert.Empty(t, stored.ApiKeys)
	assert.Equal(t, 2, stored.KeysRevision)
	revision, err := GetRiskChangeRevision()
	require.NoError(t, err)
	assert.EqualValues(t, 3, revision)
	encoded, err := common.Marshal(stored)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "api_keys")
}

func TestRiskPolicyAndConfigAtomicPublication(t *testing.T) {
	db := useRiskDB(t)
	first, err := CreateRiskPolicyRevision("baseline", "Baseline", 0, `{"skip_gpt_models":false}`, "first", 1)
	require.NoError(t, err)
	beforeConfig := `{"enabled":false,"default_binding":{"policy_key":"baseline","policy_revision":1,"mode":"observe"}}`
	_, err = PublishRiskConfig(0, beforeConfig, 1)
	require.NoError(t, err)
	afterConfig := `{"enabled":true,"default_binding":{"policy_key":"baseline","policy_revision":2,"mode":"observe"}}`
	activation := &RiskPolicyActivation{
		ExpectedConfigRevision: 1,
		BuildConfig: func(current *RiskConfig, policy *RiskPolicy) (string, error) {
			assert.Equal(t, beforeConfig, current.Payload)
			assert.Equal(t, "baseline", policy.PolicyKey)
			assert.Equal(t, 2, policy.Revision)
			return afterConfig, nil
		},
	}
	second, config, err := SaveRiskPolicyRevision("baseline", "Baseline", 1, `{"skip_gpt_models":true}`, "second", 2, activation)
	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, 2, second.Revision)
	assert.EqualValues(t, 2, config.Revision)
	assert.Equal(t, afterConfig, config.Payload)
	preserved, err := GetRiskPolicy("baseline", 1)
	require.NoError(t, err)
	assert.Equal(t, first.Payload, preserved.Payload, "audit references keep the original policy")

	changeRevision, err := GetRiskChangeRevision()
	require.NoError(t, err)
	activation.ExpectedConfigRevision = 2
	activation.BuildConfig = func(current *RiskConfig, policy *RiskPolicy) (string, error) {
		return current.Payload, nil
	}
	reused, unchanged, err := SaveRiskPolicyRevision("baseline", "Baseline", 2, second.Payload, second.Fingerprint, 3, activation)
	require.NoError(t, err)
	assert.Equal(t, second.Id, reused.Id)
	assert.Equal(t, config.Id, unchanged.Id, "repeated save and enable is idempotent")
	unchangedRevision, err := GetRiskChangeRevision()
	require.NoError(t, err)
	assert.Equal(t, changeRevision, unchangedRevision)

	for _, tc := range []struct {
		name           string
		base           int
		configRevision int64
	}{
		{name: "stale policy", base: 1, configRevision: 2},
		{name: "stale configuration", base: 2, configRevision: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			activation.ExpectedConfigRevision = tc.configRevision
			row, cfg, err := SaveRiskPolicyRevision("baseline", "Baseline", tc.base, `{}`, "third", 4, activation)
			require.ErrorIs(t, err, ErrRiskRevisionConflict)
			assert.Nil(t, row)
			assert.Nil(t, cfg)
		})
	}
	activation.ExpectedConfigRevision = 2
	rejected := errors.New("configuration preparation rejected")
	activation.BuildConfig = func(current *RiskConfig, policy *RiskPolicy) (string, error) {
		return "", rejected
	}
	row, cfg, err := SaveRiskPolicyRevision("baseline", "Baseline", 2, `{}`, "third", 4, activation)
	require.ErrorIs(t, err, rejected)
	assert.Nil(t, row)
	assert.Nil(t, cfg)

	// A failure after both inserts must roll back both, including the change
	// revision. A retry must not leave a saved-but-inactive policy behind.
	stateStatement := &gorm.Statement{DB: db}
	require.NoError(t, stateStatement.Parse(&RiskState{}))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("risk_test:reject_atomic_state", func(tx *gorm.DB) {
		if tx.Statement.Table == stateStatement.Schema.Table {
			tx.AddError(rejected)
		}
	}))
	activation.BuildConfig = func(current *RiskConfig, policy *RiskPolicy) (string, error) {
		return `{"enabled":true,"default_binding":{"policy_key":"baseline","policy_revision":3,"mode":"observe"}}`, nil
	}
	row, cfg, err = SaveRiskPolicyRevision("baseline", "Baseline", 2, `{}`, "third", 4, activation)
	require.ErrorIs(t, err, rejected)
	assert.Nil(t, row)
	assert.Nil(t, cfg)
	require.NoError(t, db.Callback().Update().Remove("risk_test:reject_atomic_state"))
	var policyCount, configCount int64
	require.NoError(t, db.Model(&RiskPolicy{}).Count(&policyCount).Error)
	require.NoError(t, db.Model(&RiskConfig{}).Count(&configCount).Error)
	assert.EqualValues(t, 2, policyCount)
	assert.EqualValues(t, 2, configCount)
	latest, err := GetLatestRiskConfig()
	require.NoError(t, err)
	assert.Equal(t, config.Id, latest.Id)
	unchangedRevision, err = GetRiskChangeRevision()
	require.NoError(t, err)
	assert.Equal(t, changeRevision, unchangedRevision)

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			<-start
			_, _, err := SaveRiskPolicyRevision("baseline", "Baseline", 2, `{}`, "third", 4, activation)
			results <- err
		})
	}
	close(start)
	workers.Wait()
	var published, conflicted int
	for range 2 {
		err := <-results
		if err == nil {
			published++
		} else {
			require.ErrorIs(t, err, ErrRiskRevisionConflict)
			conflicted++
		}
	}
	assert.Equal(t, 1, published)
	assert.Equal(t, 1, conflicted)
	require.NoError(t, db.Model(&RiskPolicy{}).Count(&policyCount).Error)
	require.NoError(t, db.Model(&RiskConfig{}).Count(&configCount).Error)
	assert.EqualValues(t, 3, policyCount)
	assert.EqualValues(t, 3, configCount)
}

func TestRiskCounterBatchRollbackAndAggregation(t *testing.T) {
	db := useRiskDB(t)
	first := RiskCounterKey{Bucket: 3600, Namespace: "a", GroupName: "vip", Route: "responses", Outcome: "pass", IsFirst: 1}
	retry := first
	retry.Namespace, retry.IsFirst = "b", 0
	deltas := map[RiskCounterKey]int64{first: 2, retry: 3}
	rejected := errors.New("second counter rejected")
	writes := 0
	counterStatement := &gorm.Statement{DB: db}
	require.NoError(t, counterStatement.Parse(&RiskCounter{}))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("risk_test:counter_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == counterStatement.Schema.Table {
			writes++
			if writes == 2 {
				tx.AddError(rejected)
			}
		}
	}))
	require.ErrorIs(t, AddRiskCounters(deltas), rejected)
	var count int64
	require.NoError(t, db.Model(&RiskCounter{}).Count(&count).Error)
	assert.Zero(t, count, "retrying a failed batch must not double earlier increments")
	require.NoError(t, db.Callback().Create().Remove("risk_test:counter_failure"))
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() { <-start; results <- AddRiskCounters(deltas) })
	}
	close(start)
	workers.Wait()
	for range 2 {
		require.NoError(t, <-results)
	}
	totals, err := SumRiskCounters(3600, 3600, "vip")
	require.NoError(t, err)
	require.Len(t, totals, 2)
	for _, row := range totals {
		if row.IsFirst == 1 {
			assert.EqualValues(t, 4, row.Hits)
		} else {
			assert.EqualValues(t, 6, row.Hits)
		}
	}
}

func TestRiskMySQLRowLockWait(t *testing.T) {
	if os.Getenv("RISK_MYSQL_TEST_DSN") == "" {
		t.Skip("RISK_MYSQL_TEST_DSN must name a disposable MySQL test database")
	}
	db := useRiskDB(t)
	require.NoError(t, ReconcileRiskGroupNamespaces(nil))
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	holder := db.WithContext(ctx).Begin()
	require.NoError(t, holder.Error)
	t.Cleanup(func() {
		if err := holder.Rollback().Error; !errors.Is(err, sql.ErrTxDone) {
			assert.NoError(t, err)
		}
	})
	state, err := lockRiskState(holder)
	require.NoError(t, err)

	// Pin a different physical connection; the short timeout is session-local.
	// Error 1205, rather than an elapsed-time comparison, proves the row lock.
	require.NoError(t, db.WithContext(ctx).Connection(func(waiter *gorm.DB) error {
		// Connection returns an initialized statement. Start a reusable session
		// on that connection so the scalar Scan does not become the lock's Model.
		waiter = waiter.Session(&gorm.Session{NewDB: true})
		var originalTimeout int
		require.NoError(t, waiter.Raw("SELECT @@SESSION.innodb_lock_wait_timeout").Scan(&originalTimeout).Error)
		require.NoError(t, waiter.Exec("SET SESSION innodb_lock_wait_timeout = 1").Error)
		defer func() {
			assert.NoError(t, waiter.Exec("SET SESSION innodb_lock_wait_timeout = ?", originalTimeout).Error)
		}()
		err := waiter.Transaction(func(tx *gorm.DB) error {
			_, err := lockRiskState(tx)
			return err
		})
		var lockError *mysqldriver.MySQLError
		require.ErrorAs(t, err, &lockError)
		assert.EqualValues(t, 1205, lockError.Number)
		require.NoError(t, holder.Model(&RiskState{}).Where("id = ?", state.Id).Update("change_revision", 10).Error)
		require.NoError(t, holder.Commit().Error)
		return waiter.Transaction(func(tx *gorm.DB) error {
			observed, err := lockRiskState(tx)
			if err != nil {
				return err
			}
			assert.EqualValues(t, 10, observed.ChangeRevision, "the waiter sees the committed holder update")
			return bumpRiskState(tx, observed, false)
		})
	}))
	revision, err := GetRiskChangeRevision()
	require.NoError(t, err)
	assert.EqualValues(t, 11, revision)
}

// requireRiskDomainUniqueness exercises each business key independently; a
// duplicate primary key must not accidentally satisfy these assertions.
func requireRiskDomainUniqueness(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, row := range []any{
		&RiskState{StateKey: "global"}, &RiskConfig{Revision: 1}, &RiskPolicy{PolicyKey: "baseline", Revision: 1},
		&RiskProvider{Name: "test"}, &RiskCounter{Bucket: 1, Namespace: "ns"},
		&RiskViolation{RequestId: "req", ContentHash: "hash"}, &RiskUserScore{UserId: 1},
	} {
		require.NoError(t, db.Create(row).Error)
		err := db.Omit("id").Create(row).Error
		require.Error(t, err)
		assert.True(t, isRiskUniqueViolation(err), "%T must enforce its business key", row)
	}
	require.NoError(t, db.Create(&RiskGroupNamespace{GroupName: "a", Namespace: "ns", Generation: 1}).Error)
	for _, row := range []RiskGroupNamespace{
		{GroupName: "a", Namespace: "different-ns", Generation: 1},
		{GroupName: "different-group", Namespace: "ns", Generation: 2},
	} {
		err := db.Create(&row).Error
		require.Error(t, err)
		assert.True(t, isRiskUniqueViolation(err), "namespace and group/generation constraints must each work")
	}
	require.NoError(t, db.Create(&RiskGroupNamespace{GroupName: "a", Namespace: "ns-v2", Generation: 2}).Error)
	require.NoError(t, db.Create(&RiskGroupNamespace{GroupName: "b", Namespace: "ns-b", Generation: 1}).Error)
}

func TestRiskExternalDatabaseMigration(t *testing.T) {
	if os.Getenv("RISK_MYSQL_TEST_DSN") == "" && os.Getenv("RISK_POSTGRES_TEST_DSN") == "" {
		t.Skip("RISK_MYSQL_TEST_DSN or RISK_POSTGRES_TEST_DSN must name a disposable test database")
	}
	db := useRiskDB(t) // The fixture performs the fresh migration.
	requireRiskDomainUniqueness(t, db)
	require.NoError(t, InsertRiskEvents([]RiskEvent{{RequestId: "preserved", Keyword: "保留现有数据", CreatedAt: 50}}))
	primaryKey := func(row any) string {
		statement := &gorm.Statement{DB: db}
		require.NoError(t, statement.Parse(row))
		return statement.Schema.PrioritizedPrimaryField.DBName
	}
	before := make([][]map[string]any, len(RiskModels()))
	for i, row := range RiskModels() {
		require.NoError(t, db.Model(row).Order(primaryKey(row)).Find(&before[i]).Error)
	}
	recorder := &migrationSQLRecorder{}
	require.NoError(t, db.Session(&gorm.Session{Logger: recorder}).AutoMigrate(RiskModels()...))
	assert.Empty(t, recorder.schemaMutations(), "second migration must not issue DDL")
	for i, row := range RiskModels() {
		var after []map[string]any
		require.NoError(t, db.Model(row).Order(primaryKey(row)).Find(&after).Error)
		assert.Equal(t, before[i], after, "%T data must survive the second migration", row)
		statement := &gorm.Statement{DB: db}
		require.NoError(t, statement.Parse(row))
		for _, index := range statement.Schema.ParseIndexes() {
			assert.True(t, db.Migrator().HasIndex(row, index.Name), "%s.%s must remain", statement.Schema.Table, index.Name)
		}
	}
	t.Log("fresh migration and second migration passed; all business uniqueness constraints, indexes, and seeded data preserved")
}

func TestRiskSQLiteMigration(t *testing.T) {
	for _, sample := range []string{"fresh", "existing"} {
		t.Run(sample, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), sample+".db")
			if sample == "existing" {
				source := os.Getenv("RISK_SQLITE_UPGRADE_COPY")
				if source == "" {
					t.Skip("RISK_SQLITE_UPGRADE_COPY must name a previously created safe backup")
				}
				resolved, err := filepath.Abs(source)
				require.NoError(t, err)
				relative, err := filepath.Rel(os.TempDir(), resolved)
				require.NoError(t, err)
				require.True(t, filepath.IsLocal(relative), "upgrade input must be a backup under the temporary directory")
				input, err := os.Open(resolved)
				require.NoError(t, err)
				output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				require.NoError(t, err)
				_, copyErr := io.Copy(output, input)
				require.NoError(t, input.Close())
				require.NoError(t, output.Close())
				require.NoError(t, copyErr)
			}
			db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			var version string
			require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
			t.Logf("SQLite %s; sample=%s", version, sample)
			type schemaEntry struct{ Type, Name, TblName, SQL string }
			var schemaBefore []schemaEntry
			require.NoError(t, db.Raw("SELECT type, name, tbl_name, sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name").Scan(&schemaBefore).Error)
			counts := map[string]int64{}
			for _, entry := range schemaBefore {
				if entry.Type == "table" {
					var n int64
					require.NoError(t, db.Table(entry.Name).Count(&n).Error)
					counts[entry.Name] = n
				}
			}
			recorder := &migrationSQLRecorder{}
			for pass := range 2 {
				recorder.reset()
				require.NoError(t, db.Session(&gorm.Session{Logger: recorder}).AutoMigrate(RiskModels()...))
				if pass == 1 {
					assert.Empty(t, recorder.schemaMutations(), "repeat migration must not mutate schema")
				}
			}
			var schemaAfter []schemaEntry
			require.NoError(t, db.Raw("SELECT type, name, tbl_name, sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name").Scan(&schemaAfter).Error)
			for _, entry := range schemaBefore {
				assert.Contains(t, schemaAfter, entry, "existing schema must remain unchanged")
			}
			for table, before := range counts {
				var after int64
				require.NoError(t, db.Table(table).Count(&after).Error)
				assert.Equal(t, before, after, table)
			}
			for _, row := range RiskModels() {
				assert.True(t, db.Migrator().HasTable(row))
			}
			if sample == "fresh" {
				// An upgrade copy already holds real rows for these keys.
				requireRiskDomainUniqueness(t, db)
			}
			t.Logf("two migrations: stable; preserved %d existing schema entries and %d tables; all domain uniqueness checks passed", len(schemaBefore), len(counts))
		})
	}
}

func TestRiskUserScoreRecovery(t *testing.T) {
	const day = int64(86400)
	for _, tc := range []struct {
		name  string
		score RiskUserScore
		at    int64
		want  int
	}{
		{"no row is full", RiskUserScore{}, 0, 100},
		{"six idle days recover nothing", RiskUserScore{UserId: 1, Score: 0}, 6 * day, 0},
		{"the seventh day recovers five", RiskUserScore{UserId: 1, Score: 0}, 7 * day, 5},
		{"each later day recovers five", RiskUserScore{UserId: 1, Score: 40}, 9 * day, 55},
		{"recovery stops at full", RiskUserScore{UserId: 1, Score: 80}, 100 * day, 100},
		{"a refund above full is capped", RiskUserScore{UserId: 1, Score: 120}, 0, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.score.Current(tc.at))
		})
	}
}

func TestRiskViolationScoringBanAndReview(t *testing.T) {
	useRiskDB(t)
	const now = int64(1_000_000)
	record := func(request, hash, mode string, deduction int, autoBan bool, at int64) (*RiskViolation, bool, bool) {
		t.Helper()
		v := &RiskViolation{RequestId: request, ContentHash: hash, UserId: 7, TokenId: 3, Mode: mode, Category: "nsfw", RequestBody: `{"messages":[]}`}
		created, ban, err := RecordRiskViolation(v, deduction, autoBan, true, at)
		require.NoError(t, err)
		return v, created, ban
	}
	for _, tc := range []struct {
		name, request, hash, mode string
		deduction                 int
		autoBan                   bool
		created, ban, duplicate   bool
		action, notification      string
		before, after             int
	}{
		{"first violation deducts", "r1", "h1", "enforce", 100, false, true, false, false, RiskActionRejected, "pending", 100, 0},
		{"same content within ten minutes is not deducted again", "r2", "h1", "enforce", 100, false, true, false, true, RiskActionRejected, "", 0, 0},
		{"a violation at zero only suggests a ban when auto ban is off", "r3", "h2", "enforce", 20, false, true, false, false, RiskActionBanSuggested, "pending", 0, 0},
		{"a violation at zero bans when auto ban is on", "r4", "h3", "enforce", 20, true, true, true, false, RiskActionAutoBanned, "pending", 0, 0},
		{"a replayed request is recorded once", "r4", "h3", "enforce", 20, true, false, false, false, RiskActionAutoBanned, "pending", 0, 0},
		{"observe mode never deducts or notifies", "r5", "h4", "observe", 20, true, true, false, false, RiskActionObserved, "", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, created, ban := record(tc.request, tc.hash, tc.mode, tc.deduction, tc.autoBan, now)
			assert.Equal(t, tc.created, created)
			assert.Equal(t, tc.ban, ban)
			assert.Equal(t, tc.duplicate, v.DuplicateOf > 0)
			assert.Equal(t, tc.action, v.Action)
			assert.Equal(t, tc.notification, v.NotificationStatus)
			assert.Equal(t, tc.before, v.ScoreBefore)
			assert.Equal(t, tc.after, v.ScoreAfter)
			assert.Equal(t, tc.before-tc.after, v.Deduction, "the deduction is the points actually removed")
		})
	}
	score, err := GetRiskUserScore(7, now)
	require.NoError(t, err)
	assert.Equal(t, 0, score)

	rows, total, err := ListRiskViolations(RiskViolationFilter{UserId: 7}, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 5, total)
	for _, listed := range rows {
		assert.Empty(t, listed.RequestBody, "lists never load evidence")
	}
	first, err := GetRiskViolation(rows[len(rows)-1].Id)
	require.NoError(t, err)
	assert.JSONEq(t, `{"messages":[]}`, first.RequestBody)

	atZero := rows[1]
	require.Equal(t, RiskActionAutoBanned, atZero.Action)
	_, err = ResolveRiskViolation(atZero.Id, 1, "revoke", "ban was wrong", now+1)
	require.NoError(t, err)
	score, err = GetRiskUserScore(7, now+1)
	require.NoError(t, err)
	assert.Equal(t, 0, score, "a violation recorded at zero removed nothing, so revoking it refunds nothing")
	revoked, err := ResolveRiskViolation(first.Id, 1, "revoke", "false positive", now+1)
	require.NoError(t, err)
	assert.Equal(t, RiskViolationRevoked, revoked.Status)
	score, err = GetRiskUserScore(7, now+1)
	require.NoError(t, err)
	assert.Equal(t, 100, score, "revoking returns the deduction")
	_, err = ResolveRiskViolation(first.Id, 1, "revoke", "again", now+2)
	assert.ErrorIs(t, err, ErrRiskViolationTransition)
	upheld, err := ResolveRiskViolation(rows[0].Id, 1, "uphold", "confirmed", now+2)
	require.NoError(t, err)
	assert.Equal(t, RiskViolationUpheld, upheld.Status)
	_, err = ResolveRiskViolation(rows[0].Id, 1, "uphold", "confirmed", now+3)
	assert.ErrorIs(t, err, ErrRiskViolationTransition)

	removed, err := CleanupRiskRecords(now+10, now+10, now+10)
	require.NoError(t, err)
	assert.EqualValues(t, 3, removed, "only reviewed violations past retention are removed")
	pending, err := CountPendingRiskViolations()
	require.NoError(t, err)
	assert.EqualValues(t, 2, pending)
}

func TestRiskViolationConcurrentDeductions(t *testing.T) {
	useRiskDB(t)
	start := make(chan struct{})
	errs := make(chan error, 4)
	var workers sync.WaitGroup
	for i := range 4 {
		workers.Go(func() {
			<-start
			v := &RiskViolation{RequestId: fmt.Sprintf("req-%d", i), ContentHash: fmt.Sprintf("hash-%d", i), UserId: 9, Mode: "enforce"}
			_, _, err := RecordRiskViolation(v, 20, false, false, 1000)
			errs <- err
		})
	}
	close(start)
	workers.Wait()
	for range 4 {
		require.NoError(t, <-errs)
	}
	score, err := GetRiskUserScore(9, 1000)
	require.NoError(t, err)
	assert.Equal(t, 20, score, "every concurrent violation deducts exactly once")
}

func TestRiskRetiredTablesAreDropped(t *testing.T) {
	db := useRiskDB(t)
	type retiredRow struct {
		Id   int
		Note string `gorm:"type:varchar(32);index"`
	}
	for _, name := range retiredRiskModels {
		table := db.NamingStrategy.TableName(name)
		require.NoError(t, db.Table(table).AutoMigrate(&retiredRow{}))
		require.NoError(t, db.Table(table).Create(&retiredRow{Note: "old"}).Error)
	}
	for range 2 {
		require.NoError(t, DropRetiredRiskTables(), "dropping is idempotent")
	}
	for _, name := range retiredRiskModels {
		assert.False(t, db.Migrator().HasTable(db.NamingStrategy.TableName(name)), name)
	}
	for _, row := range RiskModels() {
		assert.True(t, db.Migrator().HasTable(row), "%T is still used and must remain", row)
	}
}
