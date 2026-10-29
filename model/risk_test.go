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
	ns, now, policy := namespaces[0].Namespace, int64(10000), RiskBehaviorPolicy{}
	first := riskReviewCandidate(t, ns, "first", "fp1", "enforce", now)
	second := riskReviewCandidate(t, ns, "second", "fp2", "enforce", now+1)
	score, err := GetRiskTokenBehaviorScore(ns, 7, 77, policy, now+2)
	require.NoError(t, err)
	assert.Zero(t, score.Score, "pending model hits are not punishments")
	confirmed, err := ResolveRiskReview(first.Id, 1, 99, "confirm", "nsfw", "explicit generation request", policy, false, now+2)
	require.NoError(t, err)
	assert.Equal(t, 3, confirmed.Score.Score)
	assert.Nil(t, confirmed.Block)
	confirmed, err = ResolveRiskReview(second.Id, 1, 99, "confirm", "jailbreak", "explicit evasion request", policy, true, now+3)
	require.NoError(t, err)
	assert.Equal(t, 6, confirmed.Score.Score)
	require.NotNil(t, confirmed.Block)
	assert.Equal(t, "token", confirmed.Block.SubjectType)
	assert.Equal(t, "77", confirmed.Block.SubjectValue)
	assert.Equal(t, now+3+1800, confirmed.Block.ExpiresAt)
	assert.Equal(t, 2, confirmed.Score.ConfirmedEvents)

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
	assert.Equal(t, 3, revoked.Score.Score)
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
	policy := RiskBehaviorPolicy{WindowMinutes: 2, Threshold: 2, BlockTTLMinutes: 5}
	for _, test := range []struct {
		name, mode, category string
		age                  int64
		want                 int
	}{
		{"observe", "observe", "nsfw", 0, 0},
		{"ordinary", "enforce", "cyber", 0, 1},
		{"old", "enforce", "nsfw", 120, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := riskReviewCandidate(t, test.name, test.name, test.name, test.mode, 1000-test.age)
			result, err := ResolveRiskReview(row.Id, 1, 88, "confirm", test.category, "reviewed", policy, true, 1000)
			require.NoError(t, err)
			assert.Equal(t, test.want, result.Score.Score)
			assert.Nil(t, result.Block, "observe or inactive namespaces cannot produce blocks")
			assert.Equal(t, 2, result.Score.WindowMinutes)
			assert.Equal(t, 2, result.Score.Threshold)
			assert.Equal(t, 5, result.Score.BlockTTLMinutes)
		})
	}
	otherUser, err := GetRiskTokenBehaviorScore("ordinary", 8, 77, policy, 1000)
	require.NoError(t, err)
	assert.Zero(t, otherUser.Score)
	expired, err := GetRiskTokenBehaviorScore("ordinary", 7, 77, policy, 1120)
	require.NoError(t, err)
	assert.Zero(t, expired.Score)

	require.NoError(t, ReconcileRiskGroupNamespaces([]string{"disabled"}))
	namespaces, err := ListRiskGroupNamespaces(true)
	require.NoError(t, err)
	row := riskReviewCandidate(t, namespaces[0].Namespace, "disabled", "disabled", "enforce", 1000)
	result, err := ResolveRiskReview(row.Id, 1, 88, "confirm", "nsfw", "reviewed", policy, false, 1001)
	require.NoError(t, err)
	assert.Equal(t, 3, result.Score.Score)
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
	score, err := GetRiskTokenBehaviorScore("ns", 7, 77, policy, now+3)
	require.NoError(t, err)
	assert.Equal(t, 3, score.Score)
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
	useRiskDB(t)
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
	score, err := GetRiskTokenBehaviorScore(ns, 7, 77, policy, 1003)
	require.NoError(t, err)
	assert.Equal(t, 3, score.Score, "manual release does not withdraw the review decision")
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
	assert.Equal(t, 3, dismissed.Score.Score)
	refusal, err := GetRiskRefusal(ns, 7, "other-fp", 1006)
	require.NoError(t, err)
	assert.Nil(t, refusal)
	_, err = ResolveRiskReview(other.Id, 2, 99, "confirm", "nsfw", "late task", policy, true, 1007)
	require.ErrorIs(t, err, ErrRiskReviewTransition)
}

func TestRiskBehaviorMigration(t *testing.T) {
	for _, scenario := range []string{"fresh", "upgrade-v1.0.0-nw.48"} {
		t.Run(scenario, func(t *testing.T) {
			models := RiskModels()
			if scenario != "fresh" {
				models = nil
				for _, row := range RiskModels() {
					switch row.(type) {
					case *RiskReview, *RiskSignal, *RiskSubjectBlock:
					default:
						models = append(models, row)
					}
				}
			}
			db := useRiskDB(t, models)
			var oldReview riskReviewNW48
			var oldBlock riskBlockNW48
			reviewTable, blockTable := &gorm.Statement{DB: db}, &gorm.Statement{DB: db}
			require.NoError(t, reviewTable.Parse(&RiskReview{}))
			require.NoError(t, blockTable.Parse(&RiskSubjectBlock{}))
			if scenario != "fresh" {
				require.NoError(t, db.Table(reviewTable.Table).AutoMigrate(&riskReviewNW48{}))
				require.NoError(t, db.Table(blockTable.Table).AutoMigrate(&riskBlockNW48{}))
				oldReview = riskReviewNW48{RequestKey: "legacy-key", RequestId: "old-review", Namespace: "legacy-ns", UserId: 7, Mode: "enforce", Status: "confirmed", Points: 3, Revision: 2, Evidence: "保留旧版审核证据", History: "[]", CreatedAt: 1000}
				oldBlock = riskBlockNW48{Namespace: "legacy-ns", SubjectType: "user", SubjectValue: "7", Reason: "legacy", FirstSeenAt: 1000, LastSeenAt: 1000, ExpiresAt: 2000}
				require.NoError(t, db.Table(reviewTable.Table).Create(&oldReview).Error)
				require.NoError(t, db.Table(blockTable.Table).Create(&oldBlock).Error)
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
				score, err := GetRiskTokenBehaviorScore("legacy-ns", 7, 77, RiskBehaviorPolicy{}, 1001)
				require.NoError(t, err)
				assert.Zero(t, score.Score)
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

func tieredModelReview(namespace, request string, tokenID int) *RiskReview {
	return &RiskReview{Namespace: namespace, GroupName: "vip", UserId: 7, TokenId: tokenID, RequestId: request, IncidentKey: request, Fingerprint: request, Mode: "enforce", Layer: "model", Category: "nsfw", ProviderModel: "jev-test", Scores: `{"nsfw":0.1}`, Coverage: "complete", Evidence: "actual model input", OriginalRequest: "original violating user request", ConfigRevision: 1}
}

func TestRiskTieredTokenConfirmationAndManualBan(t *testing.T) {
	db := useRiskDB(t)
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
	assert.Equal(t, 3, first.Score.Score)
	assert.Nil(t, first.Block)
	second, err := CreateRiskModelReview(tieredModelReview(ns, "two", 77), policy, true, true, 1001)
	require.NoError(t, err)
	require.NotNil(t, second.Block)
	assert.Equal(t, "token", second.Block.SubjectType)
	assert.Equal(t, "77", second.Block.SubjectValue)
	assert.Equal(t, int64(2801), second.Block.ExpiresAt)
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
	assert.Equal(t, 3, otherToken.Score.Score)
	assert.Nil(t, otherToken.Block)
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
	assert.Nil(t, active)
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
	assert.Equal(t, 3, valid.Score.Score, "incomplete saved confirmations cannot contribute punishment points")
	assert.Nil(t, valid.Block)
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
	assert.Equal(t, 3, confirmed.Score.Score)
	assert.Equal(t, 1, confirmed.Score.ConfirmedEvents)
	require.NotNil(t, confirmed.Block)
	assert.Equal(t, int64(2801), confirmed.Block.ExpiresAt, "automatic punishment remains temporary")
	replayed, err := CreateRiskModelReview(&a, policy, true, true, 1002)
	require.NoError(t, err)
	assert.Equal(t, confirmed.Review.History, replayed.Review.History)
	assert.Equal(t, 3, replayed.Score.Score)
	_, err = ResolveRiskReview(confirmed.Review.Id, 2, 99, "revoke", "", "false positive", policy, false, 1003)
	require.NoError(t, err)
	a.RequestId = "late-after-revoke"
	replayed, err = CreateRiskModelReview(&a, policy, true, true, 1004)
	require.NoError(t, err)
	assert.Equal(t, RiskReviewRevoked, replayed.Review.Status)
	assert.Zero(t, replayed.Score.Score)
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
	assert.Zero(t, replayed.Score.Score)

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
