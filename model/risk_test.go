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
	provider := RiskProvider{Name: "local", ProviderType: "jev", BaseURL: "http://127.0.0.1"}
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

func TestRiskRefusalIsolationExpiryAndUpsert(t *testing.T) {
	useRiskDB(t)
	first := RiskRefusal{Namespace: "a", UserId: 1, Model: "model-a", Fingerprint: "same", FingerprintVersion: 1, Signature: "1,2", SignatureVersion: 1, LastSeenAt: 90, ExpiresAt: 100, Preview: "keep", ErrorMessage: "old", SourceRequestId: "first"}
	require.NoError(t, RememberRiskRefusal(&first))
	expired, err := GetRiskRefusal("a", 1, "same", 100)
	require.NoError(t, err)
	assert.Nil(t, expired)
	for _, rec := range []RiskRefusal{
		{Namespace: "b", UserId: 1, Model: "model-a", Fingerprint: "same", Signature: "1,2", SignatureVersion: 1, LastSeenAt: 110},
		{Namespace: "a", UserId: 2, Model: "model-a", Fingerprint: "same", Signature: "1,2", SignatureVersion: 1, LastSeenAt: 110},
		{Namespace: "a", UserId: 1, Model: "model-b", Fingerprint: "other-model", Signature: "1,2", SignatureVersion: 1, LastSeenAt: 110},
		{Namespace: "a", UserId: 1, Model: "model-a", Fingerprint: "other-version", Signature: "1,2", SignatureVersion: 2, LastSeenAt: 110},
	} {
		require.NoError(t, RememberRiskRefusal(&rec))
	}
	first.ExpiresAt, first.LastSeenAt, first.ErrorMessage, first.Preview = 0, 120, "new", ""
	require.NoError(t, RememberRiskRefusal(&first))
	first.ExpiresAt, first.LastSeenAt, first.ErrorMessage = 200, 130, ""
	require.NoError(t, RememberRiskRefusal(&first))
	stored, err := GetRiskRefusal("a", 1, "same", 1000)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Zero(t, stored.ExpiresAt)
	assert.EqualValues(t, 90, stored.FirstSeenAt)
	assert.Equal(t, "keep", stored.Preview)
	assert.Equal(t, "new", stored.ErrorMessage)
	require.NoError(t, HitRiskRefusal(stored.Id, 140))
	require.NoError(t, HitRiskRefusal(stored.Id, 141))
	hit, err := GetRiskRefusal("a", 1, "same", 1000)
	require.NoError(t, err)
	require.NotNil(t, hit)
	assert.Equal(t, 2, hit.HitCount)
	assert.EqualValues(t, 141, hit.LastSeenAt)
	assert.Zero(t, hit.ExpiresAt, "hits must not shorten a permanent refusal")
	candidates, err := ListRiskRefusalCandidates("a", 1, "model-a", 1, 1, 140)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, stored.Id, candidates[0].Id)
	n, err := ClearRiskRefusals("a", 1)
	require.NoError(t, err)
	assert.EqualValues(t, 3, n)
	other, err := GetRiskRefusal("b", 1, "same", 140)
	require.NoError(t, err)
	assert.NotNil(t, other)
	other, err = GetRiskRefusal("a", 2, "same", 140)
	require.NoError(t, err)
	assert.NotNil(t, other)
}

func TestRiskBlockReleaseRearmAndCleanup(t *testing.T) {
	db := useRiskDB(t)
	block := RiskSubjectBlock{Namespace: "a", SubjectType: "user", SubjectValue: "7", LastSeenAt: 10, ExpiresAt: 100}
	require.NoError(t, BlockRiskSubject(&block))
	stored, err := GetActiveRiskBlock("a", "user", "7", 99)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.EqualValues(t, 10, stored.FirstSeenAt)
	require.NoError(t, HitRiskBlock(stored.Id, 20))
	released, err := ReleaseRiskBlock(stored.Id, 9, 30)
	require.NoError(t, err)
	assert.True(t, released)
	released, err = ReleaseRiskBlock(stored.Id, 9, 31)
	require.NoError(t, err)
	assert.False(t, released, "releasing an already released block is idempotent")
	missing, err := GetActiveRiskBlock("a", "user", "7", 31)
	require.NoError(t, err)
	assert.Nil(t, missing)
	block.LastSeenAt, block.ExpiresAt = 40, 200
	require.NoError(t, BlockRiskSubject(&block))
	rearmed, err := GetActiveRiskBlock("a", "user", "7", 100)
	require.NoError(t, err)
	require.NotNil(t, rearmed)
	assert.Equal(t, stored.Id, rearmed.Id)
	assert.Equal(t, 1, rearmed.HitCount)
	assert.Zero(t, rearmed.ReleasedBy)
	missing, err = GetActiveRiskBlock("a", "user", "7", 200)
	require.NoError(t, err)
	assert.Nil(t, missing)
	block.Namespace, block.ExpiresAt = "b", 0
	require.NoError(t, BlockRiskSubject(&block))
	n, err := ClearRiskBlocks("a", 9, 201)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	other, err := GetActiveRiskBlock("b", "user", "7", 1000)
	require.NoError(t, err)
	assert.NotNil(t, other)

	require.NoError(t, RememberRiskRefusal(&RiskRefusal{Namespace: "a", UserId: 1, Fingerprint: "expired", LastSeenAt: 1, ExpiresAt: 200}))
	require.NoError(t, RememberRiskRefusal(&RiskRefusal{Namespace: "a", UserId: 1, Fingerprint: "permanent", LastSeenAt: 1}))
	require.NoError(t, InsertRiskEvents([]RiskEvent{{CreatedAt: 49}, {CreatedAt: 50}}))
	require.NoError(t, AddRiskCounters(map[RiskCounterKey]int64{{Bucket: 49}: 1, {Bucket: 50}: 1}))
	n, err = CleanupRiskRecords(200, 50, 50)
	require.NoError(t, err)
	assert.EqualValues(t, 4, n, "expired block/refusal and old event/counter are removed")
	var count int64
	require.NoError(t, db.Model(&RiskSubjectBlock{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	n, err = CleanupRiskRecords(200, 50, 50)
	require.NoError(t, err)
	assert.Zero(t, n)
	remainingRefusals, _, err := ListRiskRefusals(RiskListFilter{Namespace: "a"}, 0, 10)
	require.NoError(t, err)
	require.Len(t, remainingRefusals, 1)
	assert.Equal(t, "permanent", remainingRefusals[0].Fingerprint)
	remainingEvents, _, err := ListRiskEvents(RiskListFilter{}, 0, 10)
	require.NoError(t, err)
	require.Len(t, remainingEvents, 1)
	assert.EqualValues(t, 50, remainingEvents[0].CreatedAt)
	remainingCounters, err := SumRiskCounters(0, 100, "")
	require.NoError(t, err)
	require.Len(t, remainingCounters, 1)
	assert.EqualValues(t, 1, remainingCounters[0].Hits)

	block.Namespace, block.ExpiresAt, block.LastSeenAt = "a", 0, 201
	require.NoError(t, BlockRiskSubject(&block))
	recreated, err := GetActiveRiskBlock("a", "user", "7", 1000)
	require.NoError(t, err)
	require.NotNil(t, recreated)
	assert.NotEqual(t, stored.Id, recreated.Id, "a cleaned-up block creates a new record")
	assert.Zero(t, recreated.HitCount)
	assert.Zero(t, recreated.ReleasedAt)
}

func TestRiskStrikeCountWindowAndScope(t *testing.T) {
	useRiskDB(t)
	event := func(namespace string, user int, at int64, outcome, reason string) RiskEvent {
		return RiskEvent{RequestId: fmt.Sprintf("%s-%d-%d-%s", namespace, user, at, reason), Namespace: namespace, UserId: user, CreatedAt: at, Outcome: outcome, Reason: reason, Layer: "hard_regex"}
	}
	require.NoError(t, InsertRiskEvents([]RiskEvent{
		event("ns", 7, 1000, "block", "jailbreak"),
		event("ns", 7, 1100, "upstream_refusal", "content_filter"),
		event("ns", 7, 1200, "block", "subject"),
		event("ns", 7, 1300, "would_block", "jailbreak"),
		event("ns", 7, 1400, "error_block", "unavailable"),
		event("ns", 7, 100, "block", "too old"),
		event("ns", 8, 1000, "block", "other user"),
		event("other", 7, 1000, "block", "other namespace"),
	}))
	count, err := CountRiskStrikes("ns", 7, 900)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
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
		&RiskRefusal{Namespace: "ns", UserId: 1, Fingerprint: "fp"},
		&RiskSubjectBlock{Namespace: "ns", SubjectType: "user", SubjectValue: "1"},
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

func TestRiskMySQLMigration(t *testing.T) {
	if os.Getenv("RISK_MYSQL_TEST_DSN") == "" {
		t.Skip("RISK_MYSQL_TEST_DSN must name a disposable MySQL test database")
	}
	db := useRiskDB(t) // The fixture performs the fresh migration.
	requireRiskDomainUniqueness(t, db)
	require.NoError(t, InsertRiskEvents([]RiskEvent{{RequestId: "preserved", Preview: "保留现有数据", CreatedAt: 50}}))
	before := make([][]map[string]any, len(RiskModels()))
	for i, row := range RiskModels() {
		require.NoError(t, db.Model(row).Order("id").Find(&before[i]).Error)
	}
	recorder := &migrationSQLRecorder{}
	require.NoError(t, db.Session(&gorm.Session{Logger: recorder}).AutoMigrate(RiskModels()...))
	assert.Empty(t, recorder.schemaMutations(), "second migration must not issue DDL")
	for i, row := range RiskModels() {
		var after []map[string]any
		require.NoError(t, db.Model(row).Order("id").Find(&after).Error)
		assert.Equal(t, before[i], after, "%T data must survive the second migration", row)
		statement := &gorm.Statement{DB: db}
		require.NoError(t, statement.Parse(row))
		for _, index := range statement.Schema.ParseIndexes() {
			assert.True(t, db.Migrator().HasIndex(row, index.Name), "%s.%s must remain", statement.Schema.Table, index.Name)
		}
	}
	t.Log("fresh migration and second migration passed; all nine business uniqueness constraints, indexes, and seeded data preserved")
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
			requireRiskDomainUniqueness(t, db)
			t.Logf("two migrations: stable; preserved %d existing schema entries and %d tables; all nine domain uniqueness checks passed", len(schemaBefore), len(counts))
		})
	}
}

func riskReviewCandidate(t *testing.T, namespace, request, fingerprint, mode string, now int64) *RiskReview {
	t.Helper()
	row, created, err := CreateRiskReviewCandidate(&RiskReview{
		Namespace: namespace, RequestId: request, Fingerprint: fingerprint, IncidentKey: fingerprint, Mode: mode, UserId: 7, TokenId: 77,
		GroupName: "vip", Category: "nsfw", Layer: "model", Evidence: "quoted context and current user request", OriginalRequest: "current user request",
	}, RiskBehaviorPolicy{}, now)
	require.NoError(t, err)
	require.True(t, created)
	assert.Equal(t, RiskReviewPending, row.Status)
	assert.Zero(t, row.Points)
	return row
}

func TestRiskBehaviorConfirmationAndRevocation(t *testing.T) {
	db := useRiskDB(t)
	require.NoError(t, ReconcileRiskGroupNamespaces([]string{"vip"}))
	namespaces, err := ListRiskGroupNamespaces(true)
	require.NoError(t, err)
	seedHistoricalRiskReputation(t, db, 7, 60, 9999)
	ns, now, policy := namespaces[0].Namespace, int64(10000), RiskBehaviorPolicy{}
	first := riskReviewCandidate(t, ns, "first", "fp1", "enforce", now)
	second := riskReviewCandidate(t, ns, "second", "fp2", "enforce", now+1)
	score, err := GetRiskTokenBehaviorScore(ns, 7, 77, policy, now+2)
	require.NoError(t, err)
	assert.Zero(t, score.Score, "pending model hits are not punishments")
	confirmed, err := ResolveRiskReview(first.Id, 1, 99, "confirm", "nsfw", "explicit generation request", policy, false, now+2)
	require.NoError(t, err)
	assert.Equal(t, 20, confirmed.Reputation.Score)
	assert.Nil(t, confirmed.Block)
	confirmed, err = ResolveRiskReview(second.Id, 1, 99, "confirm", "jailbreak", "explicit evasion request", policy, true, now+3)
	require.NoError(t, err)
	assert.Zero(t, confirmed.Reputation.Score)
	require.NotNil(t, confirmed.Block)
	assert.Equal(t, "token", confirmed.Block.SubjectType)
	assert.Equal(t, "77", confirmed.Block.SubjectValue)
	assert.Equal(t, now+3+1800, confirmed.Block.ExpiresAt)
	assert.Equal(t, 3, confirmed.Reputation.ConfirmedEvents)

	// Same instruction and expected revision is an idempotent retry. A stale
	// instruction cannot amend the category or extend the block.
	retried, err := ResolveRiskReview(second.Id, 1, 99, "confirm", "jailbreak", "explicit evasion request", policy, true, now+4)
	require.NoError(t, err)
	assert.Equal(t, confirmed.Review.History, retried.Review.History)
	_, err = ResolveRiskReview(second.Id, 1, 99, "confirm", "nsfw", "different", policy, true, now+4)
	require.ErrorIs(t, err, ErrRiskRevisionConflict)

	refusal := RiskRefusal{Namespace: ns, UserId: 7, Fingerprint: "fp1", SourceRequestId: "first", LastSeenAt: now + 1, ExpiresAt: now + 9000}
	require.NoError(t, RememberRiskRefusal(&refusal))
	revoked, err := ResolveRiskReview(first.Id, 2, 99, "revoke", "", "medical quotation was misclassified", policy, true, now+5)
	require.NoError(t, err)
	assert.Equal(t, 20, revoked.Reputation.Score)
	assert.Equal(t, RiskReviewRevoked, revoked.Review.Status)
	block, err := GetActiveRiskBlock(ns, "token", "77", now+5)
	require.NoError(t, err)
	assert.Nil(t, block, "revoking any contributing event releases the derived block")
	require.NoError(t, RememberRiskRefusal(&refusal), "delayed callback must not resurrect a rejected refusal")
	remembered, err := GetRiskRefusal(ns, 7, "fp1", now+5)
	require.NoError(t, err)
	assert.Nil(t, remembered)
	_, err = ResolveRiskReview(first.Id, 1, 99, "confirm", "nsfw", "explicit generation request", policy, true, now+6)
	require.ErrorIs(t, err, ErrRiskRevisionConflict)
	_, err = ResolveRiskReview(first.Id, 3, 99, "confirm", "nsfw", "", policy, true, now+6)
	require.ErrorIs(t, err, ErrRiskReviewTransition)
	var savedBlock RiskSubjectBlock
	require.NoError(t, db.First(&savedBlock, confirmed.Block.Id).Error)
	assert.Equal(t, now+5, savedBlock.ReleasedAt)
	assert.Equal(t, 99, savedBlock.ReleasedBy)
}

func TestRiskBehaviorScopeWindowAndDisabledEnforcement(t *testing.T) {
	useRiskDB(t)
	policy := RiskBehaviorPolicy{}
	for _, test := range []struct {
		name, mode, category string
		age                  int64
		want                 int
	}{
		{"observe", "observe", "nsfw", 0, 100},
		{"ordinary", "enforce", "cyber", 0, 80},
		{"old", "enforce", "nsfw", 120, 60},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := riskReviewCandidate(t, test.name, test.name, test.name, test.mode, 1000-test.age)
			result, err := ResolveRiskReview(row.Id, 1, 88, "confirm", test.category, "reviewed", policy, true, 1000)
			require.NoError(t, err)
			assert.Equal(t, test.want, result.Reputation.Score)
			assert.Nil(t, result.Block, "observe or inactive namespaces cannot produce blocks")
		})
	}
	otherUser, err := GetRiskUserReputation(8, 1000)
	require.NoError(t, err)
	assert.Equal(t, 100, otherUser.Score)
	expired, err := GetRiskUserReputation(7, 1120)
	require.NoError(t, err)
	assert.Equal(t, 60, expired.Score)

	require.NoError(t, ReconcileRiskGroupNamespaces([]string{"disabled"}))
	namespaces, err := ListRiskGroupNamespaces(true)
	require.NoError(t, err)
	row := riskReviewCandidate(t, namespaces[0].Namespace, "disabled", "disabled", "enforce", 1000)
	result, err := ResolveRiskReview(row.Id, 1, 88, "confirm", "nsfw", "reviewed", policy, false, 1001)
	require.NoError(t, err)
	assert.Equal(t, 40, result.Reputation.Score)
	assert.Nil(t, result.Block, "disabled automation still computes the score")
}

func TestRiskBehaviorDeduplicationAndConcurrentReview(t *testing.T) {
	useRiskDB(t)
	policy, now := RiskBehaviorPolicy{}, int64(1000)
	first := riskReviewCandidate(t, "ns", "first", "same-fp", "enforce", now)
	alias, created, err := CreateRiskReviewCandidate(&RiskReview{Namespace: "ns", UserId: 7, TokenId: 77, RequestId: "retry", Fingerprint: "same-fp", Mode: "enforce"}, policy, now+1)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, first.Id, alias.Id)
	modelRetry, created, err := CreateRiskReviewCandidate(&RiskReview{Namespace: "ns", UserId: 7, TokenId: 77, RequestId: "different-model", Fingerprint: "model-specific-fp", IncidentKey: "same-fp", Mode: "enforce"}, policy, now+2)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, first.Id, modelRetry.Id, "switching models does not create a second scored incident")
	require.NoError(t, RememberRiskRefusal(&RiskRefusal{Namespace: "ns", UserId: 7, SourceRequestId: "different-model", Fingerprint: "model-specific-fp", LastSeenAt: now + 2}))
	// The alias is durable even after the fingerprint deduplication window ends.
	alias, created, err = CreateRiskReviewCandidate(&RiskReview{Namespace: "ns", UserId: 7, TokenId: 77, RequestId: "retry", Fingerprint: "same-fp", Mode: "enforce"}, policy, now+601)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, first.Id, alias.Id)
	rows, total, err := ListRiskReviews(RiskReviewFilter{Namespace: "ns"}, 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for operator := range 2 {
		workers.Go(func() {
			<-start
			_, err := ResolveRiskReview(first.Id, 1, operator+1, "confirm", "nsfw", "reviewed", policy, false, now+2)
			results <- err
		})
	}
	close(start)
	workers.Wait()
	success, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ErrRiskRevisionConflict)
			conflicts++
		}
	}
	assert.Equal(t, 1, success)
	assert.Equal(t, 1, conflicts)
	score, err := GetRiskUserReputation(7, now+3)
	require.NoError(t, err)
	assert.Equal(t, 80, score.Score)
	assert.Equal(t, 1, score.ConfirmedEvents)
	_, err = ResolveRiskReview(first.Id, 2, 99, "revoke", "", "false positive", policy, false, now+4)
	require.NoError(t, err)
	crossModelRefusal, err := GetRiskRefusal("ns", 7, "model-specific-fp", now+4)
	require.NoError(t, err)
	assert.Nil(t, crossModelRefusal, "revocation also removes refusals from deduplicated model switches")
	require.NoError(t, RememberRiskRefusal(&RiskRefusal{Namespace: "ns", UserId: 7, SourceRequestId: "retry", Fingerprint: "same-fp", LastSeenAt: now + 10}))
	refusal, err := GetRiskRefusal("ns", 7, "same-fp", now+10)
	require.NoError(t, err)
	assert.Nil(t, refusal, "a late callback for an aliased retry cannot resurrect the refusal")
	// New independent occurrence after the window can be reviewed, while the
	// old request ID remains a permanent retry and cannot resurrect its review.
	next := riskReviewCandidate(t, "ns", "later", "same-fp", "enforce", now+601)
	assert.NotEqual(t, first.Id, next.Id)

	t.Run("concurrent first candidate", func(t *testing.T) {
		type candidateResult struct {
			row     *RiskReview
			created bool
			err     error
		}
		start := make(chan struct{})
		results := make(chan candidateResult, 2)
		var workers sync.WaitGroup
		for range 2 {
			workers.Go(func() {
				<-start
				row, created, err := CreateRiskReviewCandidate(&RiskReview{Namespace: "new-ns", UserId: 7, TokenId: 77, RequestId: "concurrent", IncidentKey: "incident", Mode: "enforce"}, policy, now)
				results <- candidateResult{row, created, err}
			})
		}
		close(start)
		workers.Wait()
		first, second := <-results, <-results
		require.NoError(t, first.err)
		require.NoError(t, second.err)
		assert.Equal(t, first.row.Id, second.row.Id)
		assert.NotEqual(t, first.created, second.created, "only one transaction creates the candidate")
	})
}

func TestRiskBehaviorDismissalAndReleaseAreDifferent(t *testing.T) {
	db := useRiskDB(t)
	seedHistoricalRiskReputation(t, db, 7, 80, 999)
	require.NoError(t, ReconcileRiskGroupNamespaces([]string{"vip"}))
	namespaces, err := ListRiskGroupNamespaces(true)
	require.NoError(t, err)
	ns := namespaces[0].Namespace
	policy := RiskBehaviorPolicy{Threshold: 3}
	row := riskReviewCandidate(t, ns, "confirmed", "fp", "enforce", 1000)
	confirmed, err := ResolveRiskReview(row.Id, 1, 99, "confirm", "nsfw", "reviewed", policy, true, 1001)
	require.NoError(t, err)
	require.NotNil(t, confirmed.Block)
	released, err := ReleaseRiskBlock(confirmed.Block.Id, 99, 1002)
	require.NoError(t, err)
	assert.True(t, released)
	score, err := GetRiskUserReputation(7, 1003)
	require.NoError(t, err)
	assert.Equal(t, 0, score.Score, "manual release does not withdraw the review decision")
	_, err = ResolveRiskReview(row.Id, 1, 99, "confirm", "nsfw", "reviewed", policy, true, 1004)
	require.NoError(t, err)
	block, err := GetActiveRiskBlock(ns, "token", "77", 1004)
	require.NoError(t, err)
	assert.Nil(t, block, "replaying confirmation must not undo manual release")
	other := riskReviewCandidate(t, ns, "dismissed", "other-fp", "enforce", 1005)
	require.NoError(t, RememberRiskRefusal(&RiskRefusal{Namespace: ns, UserId: 7, Fingerprint: "other-fp", SourceRequestId: "dismissed", LastSeenAt: 1005}))
	dismissed, err := ResolveRiskReview(other.Id, 1, 99, "dismiss", "", "benign quotation", policy, true, 1006)
	require.NoError(t, err)
	assert.Equal(t, RiskReviewDismissed, dismissed.Review.Status)
	assert.Zero(t, dismissed.Reputation.Score)
	refusal, err := GetRiskRefusal(ns, 7, "other-fp", 1006)
	require.NoError(t, err)
	assert.Nil(t, refusal)
	_, err = ResolveRiskReview(other.Id, 2, 99, "confirm", "nsfw", "late task", policy, true, 1007)
	require.ErrorIs(t, err, ErrRiskReviewTransition)
}

func TestRiskBehaviorMigration(t *testing.T) {
	for _, scenario := range []string{"fresh", "upgrade-v1.0.0-nw.48", "upgrade-b4a8322ee"} {
		t.Run(scenario, func(t *testing.T) {
			models := RiskModels()
			if scenario != "fresh" {
				models = nil
				for _, row := range RiskModels() {
					switch row.(type) {
					case *RiskReview, *RiskSignal, *RiskSubjectBlock, *RiskReputationEntry:
					default:
						models = append(models, row)
					}
				}
			}
			db := useRiskDB(t, models)
			var previousReview riskReviewB4
			var oldReview riskReviewNW48
			var oldBlock riskBlockNW48
			reviewTable, blockTable := &gorm.Statement{DB: db}, &gorm.Statement{DB: db}
			require.NoError(t, reviewTable.Parse(&RiskReview{}))
			require.NoError(t, blockTable.Parse(&RiskSubjectBlock{}))
			if scenario != "fresh" {
				if scenario == "upgrade-b4a8322ee" {
					require.NoError(t, db.Table(reviewTable.Table).AutoMigrate(&riskReviewB4{}))
					require.NoError(t, db.AutoMigrate(&RiskSignal{}))
				} else {
					require.NoError(t, db.Table(reviewTable.Table).AutoMigrate(&riskReviewNW48{}))
				}
				if scenario == "upgrade-b4a8322ee" {
					require.NoError(t, db.AutoMigrate(&RiskSubjectBlock{}))
				} else {
					require.NoError(t, db.Table(blockTable.Table).AutoMigrate(&riskBlockNW48{}))
				}
				oldReview = riskReviewNW48{RequestKey: "legacy-key", RequestId: "old-review", Namespace: "legacy-ns", UserId: 7, Mode: "enforce", Status: "confirmed", Points: 3, Revision: 2, Evidence: "保留旧版审核证据", History: "[]", CreatedAt: 1000}
				oldBlock = riskBlockNW48{Namespace: "legacy-ns", SubjectType: "user", SubjectValue: "7", Reason: "legacy", FirstSeenAt: 1000, LastSeenAt: 1000, ExpiresAt: 2000}
				require.NoError(t, db.Table(reviewTable.Table).Create(&oldReview).Error)
				require.NoError(t, db.Table(blockTable.Table).Create(&oldBlock).Error)
				if scenario == "upgrade-b4a8322ee" {
					previousReview = riskReviewB4{RequestKey: "previous-key", RequestId: "previous-confirmed", Namespace: "legacy-ns", UserId: 7, TokenId: 77, Mode: "enforce", Status: "confirmed", Points: 3, Revision: 2, Evidence: "previous audit", OriginalRequest: "previous original", History: "[]", CreatedAt: 1000}
					require.NoError(t, db.Table(reviewTable.Table).Create(&previousReview).Error)
				}
			}
			require.NoError(t, InsertRiskEvents([]RiskEvent{{RequestId: "old-request", Preview: "保留历史事件", CreatedAt: 50}}))
			var before []RiskEvent
			require.NoError(t, db.Find(&before).Error)
			preserved := make([][]map[string]any, len(models))
			for i, row := range models {
				require.NoError(t, db.Model(row).Order("id").Find(&preserved[i]).Error)
			}
			recorder := &migrationSQLRecorder{}
			for pass := range 2 {
				recorder.reset()
				require.NoError(t, db.Session(&gorm.Session{Logger: recorder}).AutoMigrate(RiskModels()...))
				if pass == 1 {
					assert.Empty(t, recorder.schemaMutations(), "second startup must not modify schema")
				}
			}
			var after []RiskEvent
			require.NoError(t, db.Find(&after).Error)
			assert.Equal(t, before, after)
			if scenario != "fresh" {
				var reviewAfter riskReviewNW48
				var blockAfter riskBlockNW48
				require.NoError(t, db.Table(reviewTable.Table).First(&reviewAfter, oldReview.Id).Error)
				require.NoError(t, db.Table(blockTable.Table).First(&blockAfter, oldBlock.Id).Error)
				assert.Equal(t, oldReview, reviewAfter)
				assert.Equal(t, oldBlock, blockAfter)
				if scenario == "upgrade-b4a8322ee" {
					var previousAfter riskReviewB4
					require.NoError(t, db.Table(reviewTable.Table).First(&previousAfter, previousReview.Id).Error)
					assert.Equal(t, previousReview, previousAfter)
				}
				reputation, err := GetRiskUserReputation(7, 1001)
				require.NoError(t, err)
				assert.Equal(t, 100, reputation.Score, "older confirmed points never backfill deductions")
				assert.Zero(t, reputation.ConfirmedEvents)
				score, err := GetRiskTokenBehaviorScore("legacy-ns", 7, 77, RiskBehaviorPolicy{}, 1001)
				require.NoError(t, err)
				assert.Equal(t, map[bool]int{true: 3, false: 0}[scenario == "upgrade-b4a8322ee"], score.Score)
			}
			for i, row := range models {
				var current []map[string]any
				require.NoError(t, db.Model(row).Order("id").Find(&current).Error)
				assert.Equal(t, preserved[i], current, "%T existing rows must remain unchanged", row)
			}
			for _, row := range RiskModels() {
				statement := &gorm.Statement{DB: db}
				require.NoError(t, statement.Parse(row))
				for _, index := range statement.Schema.ParseIndexes() {
					assert.True(t, db.Migrator().HasIndex(row, index.Name))
				}
			}
			requireRiskDomainUniqueness(t, db)
			evidence := strings.Repeat("😀", 32769)
			row, created, err := CreateRiskReviewCandidate(&RiskReview{Namespace: "ns", UserId: 7, TokenId: 77, RequestId: "new-request", Evidence: evidence, Mode: "enforce"}, RiskBehaviorPolicy{}, 1000)
			require.NoError(t, err)
			require.True(t, created)
			saved, err := GetRiskReview(row.Id)
			require.NoError(t, err)
			assert.Equal(t, strings.Repeat("😀", 32768), saved.Evidence, "full bounded Unicode evidence survives every dialect")
			assert.True(t, saved.EvidenceTruncated)
			duplicate := *saved
			duplicate.Id = 0
			err = db.Create(&duplicate).Error
			require.Error(t, err)
			assert.True(t, isRiskUniqueViolation(err))
		})
	}
}

func TestRiskBehaviorAtomicityAndRetention(t *testing.T) {
	db := useRiskDB(t)
	seedHistoricalRiskReputation(t, db, 7, 80, 999)
	require.NoError(t, ReconcileRiskGroupNamespaces([]string{"vip"}))
	namespaces, err := ListRiskGroupNamespaces(true)
	require.NoError(t, err)
	ns := namespaces[0].Namespace
	row := riskReviewCandidate(t, ns, "original", "fp", "enforce", 1000)
	changeRevision, err := GetRiskChangeRevision()
	require.NoError(t, err)
	stale := RiskBehaviorPolicy{Threshold: 3, ExpectedChangeRevision: changeRevision + 1}
	_, err = ResolveRiskReview(row.Id, 1, 99, "confirm", "nsfw", "reviewed", stale, true, 1001)
	require.ErrorIs(t, err, ErrRiskRevisionConflict)
	policy := RiskBehaviorPolicy{Threshold: 3, ExpectedChangeRevision: changeRevision}
	injected := errors.New("injected block insert failure")
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("risk_behavior_block_failure", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "RiskSubjectBlock" {
			tx.AddError(injected)
		}
	}))
	_, err = ResolveRiskReview(row.Id, 1, 99, "confirm", "nsfw", "reviewed", policy, true, 1001)
	require.ErrorIs(t, err, injected)
	require.NoError(t, db.Callback().Create().Remove("risk_behavior_block_failure"))
	saved, err := GetRiskReview(row.Id)
	require.NoError(t, err)
	assert.Equal(t, RiskReviewPending, saved.Status, "a failed block write rolls back confirmation and score")
	assert.Equal(t, 1, saved.Revision)
	assert.Equal(t, "[]", saved.History)
	var entries int64
	require.NoError(t, db.Model(&RiskReputationEntry{}).Count(&entries).Error)
	assert.EqualValues(t, 1, entries, "failed punishment preserves only the pre-existing ledger entry")
	result, err := ResolveRiskReview(row.Id, 1, 99, "confirm", "nsfw", "reviewed", policy, true, 1001)
	require.NoError(t, err)
	require.NotNil(t, result.Block)
	_, created, err := CreateRiskReviewCandidate(&RiskReview{Namespace: ns, UserId: 7, TokenId: 77, RequestId: "later-alias", Fingerprint: "fp"}, policy, 1101)
	require.NoError(t, err)
	assert.False(t, created)
	_, err = CleanupRiskRecords(1200, 1100, 1100)
	require.NoError(t, err)
	_, err = GetRiskReview(row.Id)
	require.NoError(t, err, "live blocks must retain their review evidence")
	_, err = ReleaseRiskBlock(result.Block.Id, 99, 1201)
	require.NoError(t, err)
	_, err = CleanupRiskRecords(1202, 1100, 1100)
	require.NoError(t, err)
	_, err = GetRiskReview(row.Id)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	var remaining int64
	require.NoError(t, db.Model(&RiskReview{}).Where("namespace = ?", ns).Count(&remaining).Error)
	assert.Zero(t, remaining, "retention removes aliases together with their original, including newer aliases")
}

func TestRiskScreeningLedger(t *testing.T) {
	db := useRiskDB(t)
	policy := RiskScreeningPolicy{Threshold: 3, WindowMinutes: 10}
	zero, err := RecordRiskSignal(&RiskSignal{Namespace: "ns", UserId: 7, TokenId: 77}, policy, 999)
	require.NoError(t, err)
	assert.Zero(t, zero.Score)
	var controlRows int64
	require.NoError(t, db.Model(&RiskState{}).Count(&controlRows).Error)
	assert.Zero(t, controlRows, "zero-point lookup must not initialize or mutate control state")
	a := RiskSignal{Namespace: "ns", UserId: 7, TokenId: 77, RequestId: "first", IncidentKey: "input-a", PolicyKey: "baseline", PolicyRevision: 1, Points: 1}
	result, err := RecordRiskSignal(&a, policy, 1000)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Score)
	assert.False(t, result.ShouldAudit)
	a.RequestId = "alias"
	result, err = RecordRiskSignal(&a, policy, 1001)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Score)
	assert.Equal(t, 1, result.DistinctSignals)
	b := a
	b.RequestId = "second"
	b.IncidentKey = "input-b"
	b.Points = 2
	preview, err := PreviewRiskSignal(&b, policy, 1002)
	require.NoError(t, err)
	assert.True(t, preview.ShouldAudit)
	assert.Equal(t, 3, preview.Score)
	var count int64
	require.NoError(t, db.Model(&RiskSignal{}).Count(&count).Error)
	assert.EqualValues(t, 2, count, "preview must not write")
	result, err = RecordRiskSignal(&b, policy, 1002)
	require.NoError(t, err)
	assert.Equal(t, preview, result)
	b.RequestId = "repeat-audited"
	result, err = RecordRiskSignal(&b, policy, 1003)
	require.NoError(t, err)
	assert.True(t, result.ShouldAudit)
	assert.True(t, result.PreviouslyAudited)
	assert.Zero(t, result.Score, "replay must not add points")
	result, err = RecordRiskSignal(&a, policy, 1003)
	require.NoError(t, err)
	assert.False(t, result.ShouldAudit, "consumed weak history was not itself judged")
	clean := a
	clean.Points = 0
	result, err = RecordRiskSignal(&clean, policy, 1004)
	require.NoError(t, err)
	assert.Zero(t, result.Score)
	assert.False(t, result.ShouldAudit)
	a.TokenId = 78
	result, err = RecordRiskSignal(&a, policy, 1004)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Score)
	a.TokenId = 77
	a.PolicyRevision = 2
	result, err = RecordRiskSignal(&a, policy, 1004)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Score)
	a.PolicyRevision = 1
	result, err = RecordRiskSignal(&a, policy, 1602)
	require.NoError(t, err)
	assert.Zero(t, result.Score, "request alias remains idempotent after the window")
	a.RequestId = "new-occurrence"
	result, err = RecordRiskSignal(&a, policy, 1602)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Score)

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			<-start
			_, err := RecordRiskSignal(&RiskSignal{Namespace: "concurrent", UserId: 7, TokenId: 77, RequestId: "same", IncidentKey: "same", Points: 3}, policy, 2000)
			results <- err
		})
	}
	close(start)
	workers.Wait()
	require.NoError(t, <-results)
	require.NoError(t, <-results)
	require.NoError(t, db.Model(&RiskSignal{}).Where("namespace = ?", "concurrent").Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

// Historical deductions are immutable; use a pre-existing ledger balance when
// a regression needs to exercise restriction transitions near the threshold.
func seedHistoricalRiskReputation(t *testing.T, db *gorm.DB, userID, deduction int, at int64) {
	t.Helper()
	require.NoError(t, db.Create(&RiskReputationEntry{EventKey: fmt.Sprintf("historical-%d", userID), UserId: userID, TokenId: 999, Category: "cyber", Severity: "severe", Deduction: deduction, ConfirmedAt: at}).Error)
}

func tieredModelReview(namespace, request string, tokenID int) *RiskReview {
	return &RiskReview{Namespace: namespace, GroupName: "vip", UserId: 7, TokenId: tokenID, RequestId: request, IncidentKey: request, Fingerprint: request, Mode: "enforce", Layer: "model", Category: "nsfw", ProviderModel: "jev-test", Scores: `{"nsfw":0.1}`, Coverage: "complete", Evidence: "actual model input", OriginalRequest: "original violating user request", ConfigRevision: 1, Assessments: `[{"category":"nsfw","safety_score":0.1}]`, JudgmentVersion: RiskJudgmentVersion}
}

func TestRiskTieredTokenConfirmationAndManualBan(t *testing.T) {
	db := useRiskDB(t)
	seedHistoricalRiskReputation(t, db, 7, 80, 999)
	require.NoError(t, ReconcileRiskGroupNamespaces([]string{"vip", "other"}))
	namespaces, err := ListRiskGroupNamespaces(true)
	require.NoError(t, err)
	ns := namespaces[0].Namespace
	_, err = PublishRiskConfig(0, `{}`, 99)
	require.NoError(t, err)
	change, err := GetRiskChangeRevision()
	require.NoError(t, err)
	policy := RiskBehaviorPolicy{ExpectedConfigRevision: 1}
	first, err := CreateRiskModelReview(tieredModelReview(ns, "one", 77), policy, true, true, 1000)
	require.NoError(t, err)
	assert.Equal(t, RiskReviewConfirmed, first.Review.Status)
	assert.Equal(t, "model", first.Review.JudgmentSource)
	assert.Zero(t, first.Review.ReviewedBy)
	assert.Zero(t, first.Reputation.Score)
	assert.NotNil(t, first.Block)
	second, err := CreateRiskModelReview(tieredModelReview(ns, "two", 77), policy, true, true, 1001)
	require.NoError(t, err)
	require.NotNil(t, second.Block)
	assert.Equal(t, "token", second.Block.SubjectType)
	assert.Equal(t, "77", second.Block.SubjectValue)
	assert.Equal(t, int64(2800), second.Block.ExpiresAt)
	after, err := GetRiskChangeRevision()
	require.NoError(t, err)
	assert.Equal(t, change, after, "model confirmations must not invalidate unrelated verdict caches")
	active, err := GetActiveRiskTokenBlock(7, 77, 1002)
	require.NoError(t, err)
	require.NotNil(t, active)
	active, err = GetActiveRiskTokenBlock(8, 77, 1002)
	require.NoError(t, err)
	assert.Nil(t, active)
	active, err = GetActiveRiskTokenBlock(7, 78, 1002)
	require.NoError(t, err)
	assert.Nil(t, active)
	otherToken, err := CreateRiskModelReview(tieredModelReview(ns, "one", 78), policy, true, true, 1002)
	require.NoError(t, err)
	assert.Zero(t, otherToken.Reputation.Score)
	assert.NotNil(t, otherToken.Block)
	manual, err := CreateRiskModelReview(tieredModelReview(ns, "manual", 77), policy, false, false, 1003)
	require.NoError(t, err)
	assert.Equal(t, RiskReviewPending, manual.Review.Status)
	assert.Empty(t, manual.Review.NotificationStatus)
	ban, err := BanRiskReviewToken(manual.Review.Id, 1, 99, "nsfw", "manually confirmed", 0, policy, 1004)
	require.NoError(t, err)
	require.NotNil(t, ban.Block)
	assert.Zero(t, ban.Block.ExpiresAt)
	assert.Equal(t, "manual_review", ban.Block.Reason)
	retry, err := BanRiskReviewToken(manual.Review.Id, 1, 99, "nsfw", "manually confirmed", 0, policy, 1005)
	require.NoError(t, err)
	assert.Equal(t, ban.Review.History, retry.Review.History)
	_, err = CreateRiskModelReview(tieredModelReview(ns, "three", 77), policy, true, true, 1006)
	require.NoError(t, err)
	active, err = GetActiveRiskTokenBlock(7, 77, 1006)
	require.NoError(t, err)
	require.NotNil(t, active)
	assert.Zero(t, active.ExpiresAt, "automatic confirmation must not replace a permanent ban")
	require.NoError(t, InsertRiskEvents([]RiskEvent{{Namespace: ns, UserId: 7, RequestId: "manual", Preview: "sensitive false-positive preview"}}))
	revoked, err := ResolveRiskReview(manual.Review.Id, 2, 99, "revoke", "", "mistaken", policy, false, 1007)
	require.NoError(t, err)
	assert.Empty(t, revoked.Review.Evidence)
	assert.Empty(t, revoked.Review.OriginalRequest)
	assert.NotEmpty(t, revoked.Review.OriginalRequestHash)
	var ev RiskEvent
	require.NoError(t, db.Where("request_id = ?", "manual").First(&ev).Error)
	assert.Empty(t, ev.Preview)
	active, err = GetActiveRiskTokenBlock(7, 77, 1007)
	require.NoError(t, err)
	require.NotNil(t, active, "independent automatic confirmations remain valid after the manual basis is revoked")
	assert.Equal(t, "behavior_score", active.Reason)
	assert.Equal(t, int64(2806), active.ExpiresAt, "the automatic fallback retains its own temporary expiry")
	replayed, err := CreateRiskModelReview(tieredModelReview(ns, "manual", 77), policy, true, true, 1008)
	require.NoError(t, err)
	assert.Equal(t, RiskReviewRevoked, replayed.Review.Status)
	assert.Empty(t, replayed.Review.OriginalRequest)
	bad := tieredModelReview(ns, "incomplete", 79)
	bad.OriginalRequestTruncated = true
	pending, err := CreateRiskModelReview(bad, policy, true, true, 1008)
	require.NoError(t, err)
	assert.Equal(t, RiskReviewPending, pending.Review.Status)
	assert.Zero(t, pending.Review.Points)
	_, err = BanRiskReviewToken(pending.Review.Id, 1, 99, "nsfw", "incomplete", 30, policy, 1009)
	require.ErrorIs(t, err, ErrRiskReviewTransition)
	_, err = ResolveRiskReview(pending.Review.Id, 1, 99, "confirm", "nsfw", "incomplete", policy, true, 1009)
	require.ErrorIs(t, err, ErrRiskReviewTransition, "manual confirmation must not bypass the evidence gate")
	bypass := RiskReview{RequestKey: "bypass-row", Namespace: ns, UserId: 7, TokenId: 80, Status: RiskReviewConfirmed, Mode: "enforce", Points: 99, CreatedAt: 1008}
	require.NoError(t, db.Create(&bypass).Error)
	valid, err := CreateRiskModelReview(tieredModelReview(ns, "valid-after-bypass", 80), policy, true, true, 1009)
	require.NoError(t, err)
	assert.Equal(t, 6, valid.Reputation.ConfirmedEvents, "legacy saved confirmations cannot contribute reputation deductions")
	assert.NotNil(t, valid.Block)
	_, err = PublishRiskConfig(1, `{}`, 99)
	require.NoError(t, err)
	_, err = CreateRiskModelReview(tieredModelReview(ns, "stale-config", 77), policy, true, true, 1010)
	require.ErrorIs(t, err, ErrRiskRevisionConflict)
	rows, _, err := ListRiskReviews(RiskReviewFilter{RequestId: "stale-config"}, 0, 5)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, RiskReviewPending, rows[0].Status)
}

func TestRiskTieredModelReviewRaceAndProviderRevision(t *testing.T) {
	db := useRiskDB(t)
	require.NoError(t, ReconcileRiskGroupNamespaces([]string{"vip"}))
	namespaces, err := ListRiskGroupNamespaces(true)
	require.NoError(t, err)
	ns := namespaces[0].Namespace
	_, err = PublishRiskConfig(0, `{}`, 99)
	require.NoError(t, err)
	provider := RiskProvider{Name: "original", ProviderType: "jev", BaseURL: "https://original.invalid", Model: "jev-test"}
	require.NoError(t, SaveRiskProvider(&provider, []string{"test-only-key"}))
	expected := provider
	policy := RiskBehaviorPolicy{Threshold: 3, ExpectedConfigRevision: 1, ExpectedProvider: &expected}

	// The duplicate call B returns first but cannot auto-confirm. The original
	// call A later returns a trusted result for exactly the same evidence.
	b := tieredModelReview(ns, "replay-b", 77)
	b.IncidentKey, b.Scores = "shared-incident", `{"nsfw":0.2}`
	pending, err := CreateRiskModelReview(b, policy, false, false, 1000)
	require.NoError(t, err)
	a := *b
	a.RequestId, a.Category, a.Scores = "original-a", "jailbreak", `{"jailbreak":0.05}`
	a.Assessments = `[{"category":"jailbreak","severity":"fixed","severity_status":"fixed","safety_score":0.05}]`
	a.ScreeningScore, a.ScreeningReason = 3, "user_rule"
	confirmed, err := CreateRiskModelReview(&a, policy, true, true, 1001)
	require.NoError(t, err)
	assert.Equal(t, pending.Review.Id, confirmed.Review.Id)
	assert.Equal(t, b.RequestId, confirmed.Review.RequestId)
	assert.Equal(t, RiskReviewConfirmed, confirmed.Review.Status)
	assert.Equal(t, a.Category, confirmed.Review.Category)
	assert.Equal(t, a.Scores, confirmed.Review.Scores, "use the trusted result, not the faster replay's scores")
	assert.Equal(t, a.Evidence, confirmed.Review.Evidence)
	assert.Equal(t, a.OriginalRequest, confirmed.Review.OriginalRequest)
	assert.Equal(t, 80, confirmed.Reputation.Score)
	assert.Equal(t, 1, confirmed.Reputation.ConfirmedEvents)
	assert.Nil(t, confirmed.Block, "one confirmed violation is below the punishment threshold")
	replayed, err := CreateRiskModelReview(&a, policy, true, true, 1002)
	require.NoError(t, err)
	assert.Equal(t, confirmed.Review.History, replayed.Review.History)
	assert.Equal(t, 80, replayed.Reputation.Score)
	_, err = ResolveRiskReview(confirmed.Review.Id, 2, 99, "revoke", "", "false positive", policy, false, 1003)
	require.NoError(t, err)
	a.RequestId = "late-after-revoke"
	replayed, err = CreateRiskModelReview(&a, policy, true, true, 1004)
	require.NoError(t, err)
	assert.Equal(t, RiskReviewRevoked, replayed.Review.Status)
	assert.Equal(t, 100, replayed.Reputation.Score)
	assert.Empty(t, replayed.Review.OriginalRequest)
	block, err := GetActiveRiskTokenBlock(7, 77, 1004)
	require.NoError(t, err)
	assert.Nil(t, block)

	b = tieredModelReview(ns, "dismissed-b", 77)
	pending, err = CreateRiskModelReview(b, policy, false, false, 1005)
	require.NoError(t, err)
	_, err = ResolveRiskReview(pending.Review.Id, 1, 99, "dismiss", "", "not a violation", policy, false, 1006)
	require.NoError(t, err)
	a = *b
	a.RequestId = "late-after-dismiss"
	replayed, err = CreateRiskModelReview(&a, policy, true, true, 1007)
	require.NoError(t, err)
	assert.Equal(t, RiskReviewDismissed, replayed.Review.Status)
	assert.Empty(t, replayed.Review.Evidence)
	assert.Equal(t, 100, replayed.Reputation.Score)

	for i, tc := range []struct {
		name   string
		change func(*RiskReview)
	}{
		{"different original text", func(row *RiskReview) { row.OriginalRequest = "different original" }},
		{"different audit text", func(row *RiskReview) { row.Evidence = "different audit" }},
		{"different policy", func(row *RiskReview) { row.PolicyRevision++ }},
		{"different config", func(row *RiskReview) { row.ConfigRevision++ }},
		{"different incident under same request", func(row *RiskReview) { row.IncidentKey = "other-context" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := tieredModelReview(ns, fmt.Sprintf("mismatch-%d", i), 77)
			saved, err := CreateRiskModelReview(original, policy, false, false, 1010)
			require.NoError(t, err)
			changed := *original
			tc.change(&changed)
			_, err = CreateRiskModelReview(&changed, policy, true, true, 1011)
			require.ErrorIs(t, err, ErrRiskReviewTransition)
			row, err := GetRiskReview(saved.Review.Id)
			require.NoError(t, err)
			assert.Equal(t, RiskReviewPending, row.Status)
			assert.Equal(t, original.OriginalRequest, row.OriginalRequest)
			assert.Equal(t, original.Evidence, row.Evidence)
			assert.Zero(t, row.Points)
		})
	}

	for i, tc := range []struct {
		name   string
		change func(*RiskProvider)
		keys   []string
	}{
		{"provider type", func(row *RiskProvider) { row.ProviderType = "laya" }, nil},
		{"endpoint", func(row *RiskProvider) { row.BaseURL = "https://changed.invalid" }, nil},
		{"model", func(row *RiskProvider) { row.Model = "changed-model" }, nil},
		{"proxy", func(row *RiskProvider) { row.Proxy = "http://changed.invalid" }, nil},
		{"keys revision", func(*RiskProvider) {}, []string{"changed-test-only-key"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expected = provider
			tc.change(&provider)
			require.NoError(t, SaveRiskProvider(&provider, tc.keys))
			input := tieredModelReview(ns, fmt.Sprintf("provider-stale-%d", i), 77)
			_, err := CreateRiskModelReview(input, policy, true, true, 1020)
			require.ErrorIs(t, err, ErrRiskRevisionConflict)
			rows, _, err := ListRiskReviews(RiskReviewFilter{RequestId: input.RequestId}, 0, 5)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, RiskReviewPending, rows[0].Status)
			assert.Zero(t, rows[0].Points)
		})
	}
	expected = provider
	provider.Name = "renamed without judgment changes"
	require.NoError(t, SaveRiskProvider(&provider, nil))
	renamed, err := CreateRiskModelReview(tieredModelReview(ns, "provider-renamed", 77), policy, true, false, 1021)
	require.NoError(t, err)
	assert.Equal(t, RiskReviewConfirmed, renamed.Review.Status)
	require.NoError(t, db.Delete(&provider).Error)
	_, err = CreateRiskModelReview(tieredModelReview(ns, "provider-removed", 77), policy, true, true, 1022)
	require.ErrorIs(t, err, ErrRiskRevisionConflict)
}

func TestRiskReputationDeductionAndRevocation(t *testing.T) {
	db := useRiskDB(t)
	require.NoError(t, ReconcileRiskGroupNamespaces([]string{"one", "two"}))
	namespaces, err := ListRiskGroupNamespaces(true)
	require.NoError(t, err)
	// Old saved per-category settings are ignored, including zero and 100.
	var policy RiskBehaviorPolicy
	require.NoError(t, common.UnmarshalJsonStr(`{"deductions":{"cyber":{"mild":100,"moderate":100,"severe":100},"nsfw":{"mild":0,"moderate":0,"severe":0}},"manual_severity":"severe"}`, &policy))
	var confirmed []*RiskReviewResult
	for i, category := range []string{"cyber", "pentest", "reverse", "nsfw", "jailbreak"} {
		input := tieredModelReview(namespaces[i%2].Namespace, fmt.Sprintf("violation-%d", i), 77+i)
		input.Category = category
		if i == 0 {
			input.Severity, input.SeverityStatus = "severe", "confirmed"
			input.Assessments = `[{"category":"reverse","safety_score":0.15,"applied_deduction":99},{"category":"cyber","safety_score":0.1,"applied_deduction":1}]`
		} else {
			raw, err := common.Marshal([]RiskAssessment{{Category: category, SafetyScore: 0.1, AppliedDeduction: 100}})
			require.NoError(t, err)
			input.Assessments = string(raw)
		}
		result, err := CreateRiskModelReview(input, policy, true, true, 1000+int64(i))
		require.NoError(t, err)
		assert.Equal(t, 100-20*(i+1), result.Reputation.Score)
		assert.Equal(t, 20, result.Review.Deduction, "all categories deduct 20 once, including multi-category hits")
		assert.Empty(t, result.Review.Severity)
		assert.Empty(t, result.Review.SeverityStatus)
		assert.Equal(t, 3, result.Review.JudgmentVersion)
		var assessments []RiskAssessment
		require.NoError(t, common.UnmarshalJsonStr(result.Review.Assessments, &assessments))
		for _, assessment := range assessments {
			assert.Equal(t, 20, assessment.AppliedDeduction)
			assert.Empty(t, assessment.Severity)
		}
		if i < 4 {
			assert.Nil(t, result.Block)
		} else {
			require.NotNil(t, result.Block)
			assert.Equal(t, "81", result.Block.SubjectValue)
			assert.Equal(t, int64(2804), result.Block.ExpiresAt)
		}
		confirmed = append(confirmed, result)
	}
	_, err = CleanupRiskRecords(1005, 1001, 1001)
	require.NoError(t, err)
	retained, err := GetRiskReview(confirmed[0].Review.Id)
	require.NoError(t, err, "the other-group/token restriction retains its reputation dependencies")
	assert.NotEmpty(t, retained.OriginalRequest)
	retryInput := tieredModelReview(confirmed[4].Review.Namespace, "another-model-retry", 81)
	retryInput.IncidentKey, retryInput.Category = confirmed[4].Review.IncidentKey, "jailbreak"
	retryInput.Assessments = `[{"category":"jailbreak","safety_score":0.1}]`
	retryInput.Model, retryInput.Fingerprint = "another-model", "another-fingerprint"
	retry, err := CreateRiskModelReview(retryInput, policy, true, true, 1006)
	require.NoError(t, err)
	assert.Equal(t, confirmed[4].Review.Id, retry.Review.Id)
	assert.Equal(t, 5, retry.Reputation.ConfirmedEvents)
	revoked, err := ResolveRiskReview(confirmed[0].Review.Id, 2, 99, "revoke", "", "mistake", policy, false, 1007)
	require.NoError(t, err)
	assert.Equal(t, 20, revoked.Reputation.Score)
	active, err := GetActiveRiskTokenBlock(7, 81, 1007)
	require.NoError(t, err)
	assert.Nil(t, active, "removing a contributing deduction releases the dependent restriction")

	stale := tieredModelReview(namespaces[0].Namespace, "old-judgment", 77)
	stale.JudgmentVersion = 2
	pending, err := CreateRiskModelReview(stale, policy, true, true, 1008)
	require.NoError(t, err)
	assert.Equal(t, RiskReviewPending, pending.Review.Status, "an in-flight old judgment cannot auto-confirm under the new rules")
	assert.Zero(t, pending.Review.Deduction)
	manualInput := tieredModelReview(namespaces[0].Namespace, "manual", 77)
	manualInput.Category = "cyber"
	manualInput.Assessments = `[{"category":"cyber","safety_score":0.2,"severity":"unknown","severity_status":"missing"}]`
	pending, err = CreateRiskModelReview(manualInput, policy, false, false, 1008)
	require.NoError(t, err)
	manual, err := ResolveRiskReview(pending.Review.Id, 1, 99, "confirm", "cyber", "reviewed", policy, false, 1009)
	require.NoError(t, err)
	assert.Zero(t, manual.Reputation.Score)
	assert.Equal(t, 20, manual.Review.Deduction)
	assert.Empty(t, manual.Review.Severity, "manual confirmation no longer requires a severity")
	assert.Equal(t, manualInput.Assessments, manual.Review.Assessments, "manual decisions preserve original model evidence")
	_, err = BanRiskReviewToken(manual.Review.Id, 2, 99, "nsfw", "manual restriction", 1, policy, 1010)
	require.ErrorIs(t, err, ErrRiskReviewTransition, "a ban cannot rewrite an existing classification")
	ban, err := BanRiskReviewToken(manual.Review.Id, 2, 99, "cyber", "manual restriction", 1, policy, 1010)
	require.NoError(t, err)
	require.NotNil(t, ban.Block)
	replay, err := BanRiskReviewToken(manual.Review.Id, 2, 99, "cyber", "manual restriction", 1, policy, 1011)
	require.NoError(t, err)
	assert.Equal(t, ban.Review.History, replay.Review.History)
	_, err = CleanupRiskRecords(3000, 2999, 2999)
	require.NoError(t, err)
	reputation, err := GetRiskUserReputation(7, 3000)
	require.NoError(t, err)
	assert.Zero(t, reputation.Score)
	assert.Equal(t, 5, reputation.ConfirmedEvents)
	var remaining int64
	require.NoError(t, db.Model(&RiskReview{}).Count(&remaining).Error)
	assert.Zero(t, remaining)
	var ledger []RiskReputationEntry
	require.NoError(t, db.Order("id").Find(&ledger).Error)
	require.Len(t, ledger, 6)
	duplicate := ledger[0]
	duplicate.Id = 0
	require.Error(t, db.Create(&duplicate).Error, "the durable event remains unique after evidence cleanup")

	// A historical category-specific charge and its evidence remain unchanged
	// when an administrator explicitly bans the token under the new version.
	historical := tieredModelReview(namespaces[0].Namespace, "historical", 90)
	historical.UserId, historical.RequestKey, historical.Status, historical.Revision = 8, "historical-key", RiskReviewConfirmed, 2
	historical.Category, historical.Severity, historical.SeverityStatus, historical.Deduction, historical.JudgmentVersion = "cyber", "severe", "confirmed", 60, 2
	historical.History = "[]"
	require.NoError(t, db.Create(historical).Error)
	oldEntry := RiskReputationEntry{EventKey: "historical-entry", UserId: 8, TokenId: 90, ReviewId: historical.Id, Category: "cyber", Severity: "severe", Deduction: 60, ConfirmedAt: 900}
	require.NoError(t, db.Create(&oldEntry).Error)
	require.NoError(t, db.Model(historical).Update("reputation_entry_id", oldEntry.Id).Error)
	oldBan, err := BanRiskReviewToken(historical.Id, 2, 99, "cyber", "legacy manual ban", 1, policy, 3001)
	require.NoError(t, err)
	assert.Equal(t, 40, oldBan.Reputation.Score)
	assert.Equal(t, 60, oldBan.Review.Deduction)
	assert.Equal(t, "severe", oldBan.Review.Severity)
	assert.Equal(t, 2, oldBan.Review.JudgmentVersion)
	assert.Equal(t, 1, oldBan.Reputation.ConfirmedEvents)
}

func TestRiskReputationCrossGroupReplayAndLegacyRevocation(t *testing.T) {
	db := useRiskDB(t)
	seedHistoricalRiskReputation(t, db, 7, 80, 999)
	require.NoError(t, ReconcileRiskGroupNamespaces([]string{"one", "two", "three"}))
	namespaces, err := ListRiskGroupNamespaces(true)
	require.NoError(t, err)
	policy := RiskBehaviorPolicy{BlockTTLMinutes: 10}
	input := tieredModelReview(namespaces[0].Namespace, "same-request", 77)
	input.IncidentKey = "same-full-context"
	first, err := CreateRiskModelReview(input, policy, true, true, 1000)
	require.NoError(t, err)
	require.NotNil(t, first.Block)
	input.Namespace = namespaces[1].Namespace
	second, err := CreateRiskModelReview(input, policy, true, true, 1001)
	require.NoError(t, err)
	assert.NotEqual(t, first.Review.Id, second.Review.Id, "review provenance stays group-specific")
	assert.Equal(t, first.Review.ReputationEntryId, second.Review.ReputationEntryId)
	assert.Zero(t, second.Review.Deduction)
	assert.Equal(t, "skipped", second.Review.NotificationStatus)
	assert.Equal(t, "duplicate_incident", second.Review.NotificationError)
	assert.Nil(t, second.Block)
	input.Namespace, input.RequestId = namespaces[2].Namespace, "new-request-same-context"
	third, err := CreateRiskModelReview(input, policy, true, true, 1002)
	require.NoError(t, err)
	assert.Equal(t, first.Review.ReputationEntryId, third.Review.ReputationEntryId)
	assert.Equal(t, 2, third.Reputation.ConfirmedEvents)
	input.TokenId, input.RequestId = 78, "independent-token"
	independent, err := CreateRiskModelReview(input, policy, true, true, 1003)
	require.NoError(t, err)
	assert.Equal(t, 3, independent.Reputation.ConfirmedEvents)
	assert.NotEqual(t, first.Review.ReputationEntryId, independent.Review.ReputationEntryId)
	require.NotNil(t, independent.Block)
	active, err := GetActiveRiskTokenBlock(7, 77, 1003)
	require.NoError(t, err)
	require.NotNil(t, active)
	assert.Equal(t, int64(1600), active.ExpiresAt, "configured TTL applies and cross-group replays never extend restrictions")
	_, err = ResolveRiskReview(third.Review.Id, 2, 99, "revoke", "", "false positive", policy, false, 1004)
	require.NoError(t, err)
	for _, id := range []int{first.Review.Id, second.Review.Id, third.Review.Id} {
		row, err := GetRiskReview(id)
		require.NoError(t, err)
		assert.Equal(t, RiskReviewRevoked, row.Status)
		assert.Empty(t, row.OriginalRequest)
		assert.Empty(t, row.Evidence)
	}
	active, err = GetActiveRiskTokenBlock(7, 77, 1004)
	require.NoError(t, err)
	assert.Nil(t, active)
	active, err = GetActiveRiskTokenBlock(7, 78, 1004)
	require.NoError(t, err)
	require.NotNil(t, active, "an independently confirmed different token is not revoked")
	unchanged, err := GetRiskReview(independent.Review.Id)
	require.NoError(t, err)
	assert.Equal(t, RiskReviewConfirmed, unchanged.Status)
	assert.NotEmpty(t, unchanged.OriginalRequest)
	_, err = CreateRiskModelReview(input, policy, true, true, 3000)
	require.NoError(t, err)
	active, err = GetActiveRiskTokenBlock(7, 78, 3000)
	require.NoError(t, err)
	assert.Nil(t, active, "reading/replaying after expiry cannot reapply an old low score")

	legacy := tieredModelReview(namespaces[0].Namespace, "legacy-ban", 79)
	legacy.RequestKey, legacy.Status, legacy.Points, legacy.Revision, legacy.History = "legacy-ban-key", RiskReviewConfirmed, 3, 2, "[]"
	legacy.JudgmentVersion = 0
	require.NoError(t, db.Create(legacy).Error)
	ids, err := common.Marshal([]int{legacy.Id})
	require.NoError(t, err)
	require.NoError(t, db.Model(legacy).Update("block_review_ids", string(ids)).Error)
	block := RiskSubjectBlock{Namespace: legacy.Namespace, UserId: 7, SubjectType: "token", SubjectValue: "79", Reason: "behavior_score", SourceRequestId: fmt.Sprintf("risk-review:%d", legacy.Id), ExpiresAt: 5000}
	require.NoError(t, db.Create(&block).Error)
	_, err = ResolveRiskReview(legacy.Id, 2, 99, "revoke", "", "legacy mistake", policy, false, 3001)
	require.NoError(t, err)
	active, err = GetActiveRiskTokenBlock(7, 79, 3001)
	require.NoError(t, err)
	assert.Nil(t, active, "legacy blocks still revoke without a reputation ledger entry")
}

func TestRiskReputationConfirmationOrder(t *testing.T) {
	db := useRiskDB(t)
	seedHistoricalRiskReputation(t, db, 7, 60, 998)
	require.NoError(t, ReconcileRiskGroupNamespaces([]string{"ordered"}))
	namespaces, err := ListRiskGroupNamespaces(true)
	require.NoError(t, err)
	policy := RiskBehaviorPolicy{}
	firstInput := tieredModelReview(namespaces[0].Namespace, "finishes-first", 77)
	firstInput.Category, firstInput.Severity, firstInput.SeverityStatus = "cyber", "severe", "confirmed"
	firstInput.Assessments = `[{"category":"cyber","severity":"severe","severity_status":"confirmed"}]`
	first, err := CreateRiskModelReview(firstInput, policy, true, true, 1001)
	require.NoError(t, err)
	assert.Equal(t, 20, first.Reputation.Score)
	secondInput := *firstInput
	secondInput.RequestId, secondInput.IncidentKey, secondInput.Fingerprint, secondInput.TokenId = "finishes-late", "late-context", "late-fingerprint", 78
	second, err := CreateRiskModelReview(&secondInput, policy, true, true, 1000)
	require.NoError(t, err)
	assert.Zero(t, second.Reputation.Score, "a delayed confirmation includes the already committed deduction")
	assert.Equal(t, int64(1001), second.Reputation.LastViolationAt)
	require.NotNil(t, second.Block)
	assert.Equal(t, "78", second.Block.SubjectValue)
	var entries []RiskReputationEntry
	require.NoError(t, db.Order("id asc").Find(&entries).Error)
	require.Len(t, entries, 3)
	assert.Equal(t, entries[1].ConfirmedAt, entries[2].ConfirmedAt)
	_, err = ResolveRiskReview(first.Review.Id, 2, 99, "revoke", "", "mistake", policy, false, 1002)
	require.NoError(t, err)
	_, err = ResolveRiskReview(second.Review.Id, 2, 99, "revoke", "", "mistake", policy, false, 1003)
	require.NoError(t, err)
	thirdInput := tieredModelReview(namespaces[0].Namespace, "after-revocation", 79)
	third, err := CreateRiskModelReview(thirdInput, policy, true, true, 999)
	require.NoError(t, err)
	assert.Equal(t, int64(1001), third.Reputation.LastViolationAt, "revoked entries retain only the monotonic timestamp boundary")
	assert.Equal(t, 2, third.Reputation.ConfirmedEvents)
	assert.Equal(t, 80, third.Reputation.TotalDeducted)
}

func TestRiskReputationIndependentManualRestrictions(t *testing.T) {
	db := useRiskDB(t)
	require.NoError(t, ReconcileRiskGroupNamespaces([]string{"manual"}))
	namespaces, err := ListRiskGroupNamespaces(true)
	require.NoError(t, err)
	ns := namespaces[0].Namespace
	policy := RiskBehaviorPolicy{}
	for index, tc := range []struct {
		name          string
		secondTTL     int
		revoke        string
		releaseAll    bool
		legacyRelease bool
		rearmExisting bool
	}{
		{name: "revoke weaker basis", revoke: "second"},
		{name: "revoke primary basis", revoke: "first"},
		{name: "fallback keeps original expiry", secondTTL: 5, revoke: "first"},
		{name: "explicit release does not resurrect old bases", revoke: "release"},
		{name: "clear does not resurrect old bases", revoke: "release", releaseAll: true},
		{name: "legacy release does not resurrect old bases", revoke: "release", legacyRelease: true},
		{name: "legacy release before rearming same review", revoke: "release", legacyRelease: true, rearmExisting: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tokenID := 100 + index
			userID := 200 + index
			createBan := func(request string, at int64, ttl int) *RiskReviewResult {
				input := tieredModelReview(ns, fmt.Sprintf("%d-%s", tokenID, request), tokenID)
				input.UserId = userID
				pending, err := CreateRiskModelReview(input, policy, false, false, at)
				require.NoError(t, err)
				result, err := BanRiskReviewToken(pending.Review.Id, 1, 99, "nsfw", request, ttl, policy, at)
				require.NoError(t, err)
				require.NotNil(t, result.Block)
				return result
			}
			first := createBan("first", 1000, 0)
			second := createBan("second", 1001, tc.secondTTL)
			assert.Equal(t, first.Block.SourceRequestId, second.Block.SourceRequestId, "an equal or stronger independent basis is preserved")
			switch tc.revoke {
			case "second":
				_, err := ResolveRiskReview(second.Review.Id, 2, 99, "revoke", "", "mistake", policy, false, 1002)
				require.NoError(t, err)
				active, err := GetActiveRiskTokenBlock(userID, tokenID, 1002)
				require.NoError(t, err)
				require.NotNil(t, active)
				assert.Equal(t, first.Block.SourceRequestId, active.SourceRequestId)
			case "first":
				_, err := ResolveRiskReview(first.Review.Id, 2, 99, "revoke", "", "mistake", policy, false, 1002)
				require.NoError(t, err)
				active, err := GetActiveRiskTokenBlock(userID, tokenID, 1002)
				require.NoError(t, err)
				require.NotNil(t, active)
				assert.Equal(t, fmt.Sprintf("risk-review:%d", second.Review.Id), active.SourceRequestId)
				if tc.secondTTL == 0 {
					assert.Zero(t, active.ExpiresAt)
				} else {
					assert.Equal(t, int64(1001+tc.secondTTL*60), active.ExpiresAt)
				}
			case "release":
				if tc.legacyRelease {
					for _, previous := range []*RiskReview{first.Review, second.Review} {
						var history []riskReviewHistory
						require.NoError(t, common.UnmarshalJsonStr(previous.History, &history))
						for i := range history {
							history[i].BlockId, history[i].BlockExpiresAt = 0, 0
						}
						encoded, err := common.Marshal(history)
						require.NoError(t, err)
						require.NoError(t, db.Model(previous).Update("history", string(encoded)).Error)
					}
					require.NoError(t, db.Model(&RiskSubjectBlock{}).Where("id = ?", first.Block.Id).Updates(map[string]any{"released_at": 1002, "released_by": 99}).Error)
				} else if tc.releaseAll {
					changed, err := ClearRiskBlocks(ns, 99, 1002)
					require.NoError(t, err)
					assert.Positive(t, changed)
				} else {
					changed, err := ReleaseRiskBlock(first.Block.Id, 99, 1002)
					require.NoError(t, err)
					assert.True(t, changed)
				}
				var third *RiskReviewResult
				if tc.rearmExisting {
					third, err = BanRiskReviewToken(second.Review.Id, 2, 99, "nsfw", "third", 0, policy, 1010)
					require.NoError(t, err)
				} else {
					third = createBan("third", 1010, 0)
				}
				_, err := ResolveRiskReview(third.Review.Id, third.Review.Revision, 99, "revoke", "", "mistake", policy, false, 1011)
				require.NoError(t, err)
				active, err := GetActiveRiskTokenBlock(userID, tokenID, 1011)
				require.NoError(t, err)
				assert.Nil(t, active, "an explicit release applies to all existing manual bases")
			}
		})
	}
}

func TestRiskReputationIndependentAutomaticRestrictions(t *testing.T) {
	db := useRiskDB(t)
	require.NoError(t, ReconcileRiskGroupNamespaces([]string{"first", "second"}))
	namespaces, err := ListRiskGroupNamespaces(true)
	require.NoError(t, err)
	for index, tc := range []struct {
		name              string
		manualFirst       bool
		secondEnabled     bool
		explicitlyRelease bool
		releaseAll        bool
		secondTTL         int
		wantExpiry        int64
	}{
		{name: "independent confirmation survives revocation", secondEnabled: true, secondTTL: 30, wantExpiry: 2800},
		{name: "disabled confirmation is not a restriction basis", secondTTL: 30},
		{name: "shorter automatic basis keeps its own expiry", secondEnabled: true, secondTTL: 1, wantExpiry: 1061},
		{name: "manual permanent never makes automatic permanent", manualFirst: true, secondEnabled: true, secondTTL: 30, wantExpiry: 2801},
		{name: "explicit release suppresses automatic bases", secondEnabled: true, explicitlyRelease: true, secondTTL: 30},
		{name: "clear suppresses automatic bases", secondEnabled: true, explicitlyRelease: true, releaseAll: true, secondTTL: 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userID, tokenID := 500+index, 600+index
			seedHistoricalRiskReputation(t, db, userID, 80, 999)
			policy := RiskBehaviorPolicy{}
			input := tieredModelReview(namespaces[0].Namespace, fmt.Sprintf("%d-first", tokenID), tokenID)
			input.UserId = userID
			first, err := CreateRiskModelReview(input, policy, !tc.manualFirst, true, 1000)
			if tc.manualFirst {
				require.NoError(t, err)
				first, err = BanRiskReviewToken(first.Review.Id, 1, 99, "nsfw", "permanent", 0, policy, 1000)
			}
			require.NoError(t, err)
			require.NotNil(t, first.Block)
			secondInput := tieredModelReview(namespaces[1].Namespace, fmt.Sprintf("%d-second", tokenID), tokenID)
			secondInput.UserId = userID
			secondPolicy := RiskBehaviorPolicy{BlockTTLMinutes: tc.secondTTL}
			second, err := CreateRiskModelReview(secondInput, secondPolicy, true, tc.secondEnabled, 1001)
			require.NoError(t, err)
			assert.Equal(t, 3, second.Reputation.ConfirmedEvents)
			if tc.manualFirst {
				_, err := CleanupRiskRecords(1002, 1002, 1002)
				require.NoError(t, err)
				retained, err := GetRiskReview(second.Review.Id)
				require.NoError(t, err, "manual restrictions retain another group's active automatic basis")
				assert.NotEmpty(t, retained.OriginalRequest)
			}
			if tc.explicitlyRelease {
				if tc.releaseAll {
					changed, err := ClearRiskBlocks(first.Block.Namespace, 99, 1002)
					require.NoError(t, err)
					assert.Positive(t, changed)
				} else {
					changed, err := ReleaseRiskBlock(first.Block.Id, 99, 1002)
					require.NoError(t, err)
					assert.True(t, changed)
				}
				thirdInput := tieredModelReview(namespaces[0].Namespace, fmt.Sprintf("%d-third", tokenID), tokenID)
				thirdInput.UserId = userID
				third, err := CreateRiskModelReview(thirdInput, policy, true, true, 1003)
				require.NoError(t, err)
				require.NotNil(t, third.Block)
				_, err = ResolveRiskReview(third.Review.Id, 2, 99, "revoke", "", "mistake", policy, false, 1004)
				require.NoError(t, err)
			} else {
				_, err := ResolveRiskReview(first.Review.Id, 2, 99, "revoke", "", "mistake", policy, false, 1003)
				require.NoError(t, err)
			}
			active, err := GetActiveRiskTokenBlock(userID, tokenID, 1004)
			require.NoError(t, err)
			if tc.wantExpiry == 0 {
				assert.Nil(t, active, "only explicitly authorized and unreleased bases can survive")
			} else {
				require.NotNil(t, active)
				assert.Equal(t, fmt.Sprintf("risk-review:%d", second.Review.Id), active.SourceRequestId)
				assert.Equal(t, "behavior_score", active.Reason)
				assert.Equal(t, tc.wantExpiry, active.ExpiresAt)
			}
		})
	}
}

func TestRiskReputationRecovery(t *testing.T) {
	const start int64 = 1000
	entries := []RiskReputationEntry{{Id: 1, Deduction: 60, ConfirmedAt: start}}
	for _, tc := range []struct {
		name             string
		at               int64
		score, recovered int
		next             int64
	}{
		{"before seven days", start + 7*86400 - 1, 40, 0, start + 7*86400},
		{"first recovery", start + 7*86400, 45, 5, start + 8*86400},
		{"next full day", start + 8*86400, 50, 10, start + 9*86400},
		{"at ceiling", start + 18*86400, 100, 60, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			summary := replayRiskReputation(entries, tc.at)
			assert.Equal(t, tc.score, summary.Score)
			assert.Equal(t, tc.recovered, summary.RecoveredPoints)
			assert.Equal(t, tc.next, summary.NextRecoveryAt)
		})
	}
	entries = append(entries, RiskReputationEntry{Id: 2, Deduction: 20, ConfirmedAt: start + 8*86400})
	summary := replayRiskReputation(entries, start+14*86400)
	assert.Equal(t, 30, summary.Score)
	assert.Equal(t, start+15*86400, summary.NextRecoveryAt, "a new confirmation restarts the seven-day clock")
	entries[1].RevokedAt = start + 14*86400
	summary = replayRiskReputation(entries, start+14*86400)
	assert.Equal(t, 80, summary.Score, "revocation restores the recovery schedule of valid events")
	assert.Equal(t, 40, summary.RecoveredPoints)
}

func TestRiskTieredNotifications(t *testing.T) {
	useRiskDB(t)
	policy := RiskBehaviorPolicy{}
	observe := tieredModelReview("ns", "observe", 77)
	observe.Mode = "observe"
	row, err := CreateRiskModelReview(observe, policy, false, false, 1000)
	require.NoError(t, err)
	_, err = ResolveRiskReview(row.Review.Id, 1, 99, "confirm", "nsfw", "observed", policy, false, 1001)
	require.NoError(t, err)
	claim, err := ClaimRiskReviewNotification(1001)
	require.NoError(t, err)
	assert.Nil(t, claim)
	confirmed, err := CreateRiskModelReview(tieredModelReview("ns", "mail", 77), policy, true, false, 1002)
	require.NoError(t, err)
	assert.Equal(t, "pending", confirmed.Review.NotificationStatus)
	start := make(chan struct{})
	claims := make(chan *RiskReview, 2)
	errs := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() { <-start; row, err := ClaimRiskReviewNotification(1003); claims <- row; errs <- err })
	}
	close(start)
	workers.Wait()
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	a, b := <-claims, <-claims
	assert.NotEqual(t, a == nil, b == nil)
	require.NoError(t, FinishRiskReviewNotification(confirmed.Review.Id, "sent", "", 1004))
	claim, err = ClaimRiskReviewNotification(1005)
	require.NoError(t, err)
	assert.Nil(t, claim)
	next, err := CreateRiskModelReview(tieredModelReview("ns", "expired-claim", 77), policy, true, false, 1006)
	require.NoError(t, err)
	claim, err = ClaimRiskReviewNotification(1007)
	require.NoError(t, err)
	require.NotNil(t, claim)
	claim, err = ClaimRiskReviewNotification(1308)
	require.NoError(t, err)
	assert.Nil(t, claim)
	saved, err := GetRiskReview(next.Review.Id)
	require.NoError(t, err)
	assert.Equal(t, "failed", saved.NotificationStatus)
	assert.Equal(t, "claim_expired", saved.NotificationError)
	require.NoError(t, FinishRiskReviewNotification(saved.Id, "sent", "", 1309))
	saved, err = GetRiskReview(saved.Id)
	require.NoError(t, err)
	assert.Equal(t, "failed", saved.NotificationStatus)
	last, err := CreateRiskModelReview(tieredModelReview("ns", "withdrawn", 77), policy, true, false, 1310)
	require.NoError(t, err)
	_, err = ResolveRiskReview(last.Review.Id, 2, 99, "revoke", "", "mistake", policy, false, 1311)
	require.NoError(t, err)
	claim, err = ClaimRiskReviewNotification(1312)
	require.NoError(t, err)
	assert.Nil(t, claim)
	saved, err = GetRiskReview(last.Review.Id)
	require.NoError(t, err)
	assert.Equal(t, "skipped", saved.NotificationStatus)
	require.ErrorIs(t, FinishRiskReviewNotification(last.Review.Id, "failed", "raw SMTP diagnostic", 1313), ErrRiskReviewTransition)
}

// Exact v1.0.0-nw.48 definitions for the real upgrade fixture.
type riskReviewNW48 struct {
	Id                int    `json:"id"`
	RequestKey        string `json:"-" gorm:"type:varchar(64);uniqueIndex:idx_risk_review_request"`
	DuplicateOf       int    `json:"-"`
	RequestId         string `json:"request_id" gorm:"type:varchar(64)"`
	CreatedAt         int64  `json:"created_at" gorm:"bigint;index:idx_risk_review_scope,priority:3"`
	Namespace         string `json:"namespace" gorm:"type:varchar(32);index:idx_risk_review_scope,priority:1"`
	GroupName         string `json:"group_name" gorm:"type:varchar(128)"`
	UserId            int    `json:"user_id" gorm:"index:idx_risk_review_scope,priority:2"`
	Mode              string `json:"mode" gorm:"type:varchar(12)"`
	Layer             string `json:"layer" gorm:"type:varchar(16)"`
	Category          string `json:"category" gorm:"type:varchar(32)"`
	Reason            string `json:"reason" gorm:"type:varchar(64)"`
	Model             string `json:"model" gorm:"type:varchar(128)"`
	ProviderModel     string `json:"provider_model" gorm:"type:varchar(120)"`
	Scores            string `json:"scores" gorm:"type:text"`
	PolicyKey         string `json:"policy_key" gorm:"type:varchar(64)"`
	PolicyRevision    int    `json:"policy_revision"`
	Fingerprint       string `json:"fingerprint" gorm:"type:varchar(64)"`
	IncidentKey       string `json:"incident_key" gorm:"type:varchar(64)"`
	Evidence          string `json:"evidence" gorm:"size:131072"`
	EvidenceTruncated bool   `json:"evidence_truncated"`
	Coverage          string `json:"coverage" gorm:"type:varchar(16)"`
	Status            string `json:"status" gorm:"type:varchar(12);index:idx_risk_review_status"`
	Points            int    `json:"points"`
	Revision          int    `json:"revision"`
	ReviewedAt        int64  `json:"reviewed_at" gorm:"bigint"`
	ReviewedBy        int    `json:"reviewed_by"`
	ReviewNote        string `json:"review_note" gorm:"type:varchar(500)"`
	History           string `json:"history" gorm:"type:text"`
	BlockReviewIds    string `json:"-" gorm:"type:text"`
}

type riskBlockNW48 struct {
	Id              int    `json:"id"`
	Namespace       string `json:"namespace" gorm:"type:varchar(32);uniqueIndex:idx_risk_block_key,priority:1"`
	SubjectType     string `json:"subject_type" gorm:"type:varchar(12);uniqueIndex:idx_risk_block_key,priority:2"`
	SubjectValue    string `json:"subject_value" gorm:"type:varchar(80);uniqueIndex:idx_risk_block_key,priority:3"`
	Reason          string `json:"reason" gorm:"type:varchar(80)"`
	Fingerprint     string `json:"fingerprint" gorm:"type:varchar(64)"`
	SourceRequestId string `json:"source_request_id" gorm:"type:varchar(64)"`
	HitCount        int    `json:"hit_count"`
	FirstSeenAt     int64  `json:"first_seen_at" gorm:"bigint"`
	LastSeenAt      int64  `json:"last_seen_at" gorm:"bigint"`
	ExpiresAt       int64  `json:"expires_at" gorm:"bigint"`
	ReleasedAt      int64  `json:"released_at" gorm:"bigint"`
	ReleasedBy      int    `json:"released_by"`
}

// Exact b4a8322ee review schema, before reputation fields.
type riskReviewB4 struct {
	Id                       int    `json:"id"`
	RequestKey               string `json:"-" gorm:"type:varchar(64);uniqueIndex:idx_risk_review_request"`
	DuplicateOf              int    `json:"-"`
	RequestId                string `json:"request_id" gorm:"type:varchar(64)"`
	CreatedAt                int64  `json:"created_at" gorm:"bigint;index:idx_risk_review_scope,priority:3"`
	Namespace                string `json:"namespace" gorm:"type:varchar(32);index:idx_risk_review_scope,priority:1"`
	GroupName                string `json:"group_name" gorm:"type:varchar(128)"`
	UserId                   int    `json:"user_id" gorm:"index:idx_risk_review_scope,priority:2"`
	TokenId                  int    `json:"token_id"`
	JudgmentSource           string `json:"judgment_source" gorm:"type:varchar(12)"`
	ScreeningScore           int    `json:"screening_score"`
	ScreeningReason          string `json:"screening_reason" gorm:"type:varchar(64)"`
	ConfigRevision           int64  `json:"config_revision" gorm:"bigint"`
	OriginalRequest          string `json:"original_request" gorm:"size:1048576"`
	OriginalRequestTruncated bool   `json:"original_request_truncated"`
	OriginalRequestHash      string `json:"original_request_hash" gorm:"type:varchar(64)"`
	NotificationStatus       string `json:"notification_status" gorm:"type:varchar(12);index:idx_risk_review_notification"`
	NotificationAt           int64  `json:"notification_at" gorm:"bigint"`
	NotificationError        string `json:"notification_error" gorm:"type:varchar(48)"`
	Mode                     string `json:"mode" gorm:"type:varchar(12)"`
	Layer                    string `json:"layer" gorm:"type:varchar(16)"`
	Category                 string `json:"category" gorm:"type:varchar(32)"`
	Reason                   string `json:"reason" gorm:"type:varchar(64)"`
	Model                    string `json:"model" gorm:"type:varchar(128)"`
	ProviderModel            string `json:"provider_model" gorm:"type:varchar(120)"`
	Scores                   string `json:"scores" gorm:"type:text"`
	PolicyKey                string `json:"policy_key" gorm:"type:varchar(64)"`
	PolicyRevision           int    `json:"policy_revision"`
	Fingerprint              string `json:"fingerprint" gorm:"type:varchar(64)"`
	IncidentKey              string `json:"incident_key" gorm:"type:varchar(64)"`
	Evidence                 string `json:"evidence" gorm:"size:131072"`
	EvidenceTruncated        bool   `json:"evidence_truncated"`
	Coverage                 string `json:"coverage" gorm:"type:varchar(16)"`
	Status                   string `json:"status" gorm:"type:varchar(12);index:idx_risk_review_status"`
	Points                   int    `json:"points"`
	Revision                 int    `json:"revision"`
	ReviewedAt               int64  `json:"reviewed_at" gorm:"bigint"`
	ReviewedBy               int    `json:"reviewed_by"`
	ReviewNote               string `json:"review_note" gorm:"type:varchar(500)"`
	History                  string `json:"history" gorm:"type:text"`
	BlockReviewIds           string `json:"-" gorm:"type:text"`
}
