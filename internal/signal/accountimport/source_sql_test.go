//go:build linux && (cgo || libsignal_go)

package accountimport_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/accountimport"
)

const sourceSQLRefusal = "refusal"

const sourceSQLCancellation = "cancellation"

const sourceRetryFalse = "INSERT INTO key_value(key,value) VALUES('retry-failed-messages',0)"

// Materialize the committed Java schema and protocol records, never a Java loader.
func newSQLSnapshotTree(t *testing.T) snapshotTree {
	t.Helper()
	tree := newSnapshotTree(t)
	check(t, os.Remove(tree.database))

	database := openSQLFixture(t, tree.database)
	for _, name := range []string{"schema.sql", "rows.sql"} {
		raw := must(os.ReadFile(filepath.Join("testdata", "java-0.103.0", name)))(t)
		_, err := database.ExecContext(t.Context(), string(raw))
		check(t, err)
	}

	_, err := database.ExecContext(t.Context(), sourceRetryFalse)
	check(t, err)
	check(t, database.Close())

	return tree
}

func openSQLFixture(t *testing.T, path string) *sql.DB {
	t.Helper()
	database := must(sql.Open(sourceTestDriver, path))(t)
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { check(t, database.Close()) })

	return database
}

func editSQLFixture(t *testing.T, tree snapshotTree, statement string) {
	t.Helper()
	database := openSQLFixture(t, tree.database)
	_, err := database.ExecContext(t.Context(), statement)
	check(t, err)
	check(t, database.Close())
}

func validateSQLFixture(t *testing.T, tree snapshotTree, opts accountimport.SnapshotTestOptions, want error) {
	t.Helper()
	before := snapshotFiles(t, tree.source)
	snap := must(accountimport.OpenSnapshotForTest(t.Context(), tree.options(), opts))(t)

	err := snap.ValidateSourceForTest(t.Context())
	if !errors.Is(err, want) {
		t.Errorf("validation = %v, want %v", err, want)
	}

	if err != nil && strings.Contains(err.Error(), "secret-source-value") {
		t.Error("source value leaked")
	}

	check(t, snap.Revalidate(t.Context()))
	check(t, snap.Close())

	if after := snapshotFiles(t, tree.source); !reflect.DeepEqual(before, after) {
		t.Error("SQL reader changed source")
	}

	assertNoSnapshotWorkspace(t, tree)
}

// Removing schema, type, integrity or pre-allocation gates must fail these cases.
//
//nolint:funlen // Keep schema and record refusal cases in one matrix.
func TestAccountImportSourceSQL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, sql string
		want      error
	}{
		{"valid Java records", "", nil},
		{
			"corrupt strict row", "PRAGMA writable_schema=ON; UPDATE sqlite_master SET sql=" +
				"replace(sql,'pack_key BLOB NOT NULL','pack_key ANY NOT NULL') WHERE name='sticker';" +
				"PRAGMA schema_version=100; INSERT INTO sticker(pack_id,pack_key) " +
				"VALUES(X'00','secret-source-value'); UPDATE sqlite_master SET sql=" +
				"replace(sql,'pack_key ANY NOT NULL','pack_key BLOB NOT NULL') WHERE name='sticker';" +
				"PRAGMA schema_version=101;", signal.ErrInvalidAccountImport,
		},
		{
			"wrong marker", "PRAGMA user_version=30",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"new marker", "PRAGMA user_version=32",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"missing table", "DROP TABLE identity",
			signal.ErrInvalidAccountImport,
		},
		{
			"extra table", "CREATE TABLE unexpected(secret TEXT)",
			signal.ErrInvalidAccountImport,
		},
		{
			"view", "CREATE VIEW secret_source_value AS SELECT * FROM identity",
			signal.ErrInvalidAccountImport,
		},
		{
			"trigger", "CREATE TRIGGER secret_source_value AFTER INSERT ON identity " +
				"BEGIN DELETE FROM identity; END",
			signal.ErrInvalidAccountImport,
		},
		{
			"missing index", "DROP INDEX msl_content_index",
			signal.ErrInvalidAccountImport,
		},
		{
			"changed index", "DROP INDEX msl_content_index; CREATE INDEX msl_content_index ON " +
				"message_send_log(address)",
			signal.ErrInvalidAccountImport,
		},
		{
			"extra constraint", "PRAGMA writable_schema=ON; UPDATE sqlite_master SET " +
				"sql=replace(sql,'address TEXT UNIQUE NOT NULL','address TEXT " +
				"UNIQUE NOT NULL CHECK(length(address)>0)') WHERE " +
				"name='identity'; PRAGMA schema_version=100",
			signal.ErrInvalidAccountImport,
		},
		{
			"wrong declared type", "PRAGMA writable_schema=ON; UPDATE sqlite_master SET " +
				"sql=replace(sql,'added_timestamp INTEGER','added_timestamp " +
				"TEXT') WHERE name='identity'; PRAGMA schema_version=100",
			signal.ErrInvalidAccountImport,
		},
		{
			"missing unique", "PRAGMA writable_schema=ON; UPDATE sqlite_master SET " +
				"sql=replace(sql,'address TEXT UNIQUE NOT NULL','address TEXT NOT " +
				"NULL') WHERE name='identity'; PRAGMA schema_version=100",
			signal.ErrInvalidAccountImport,
		},
		{
			"unsupported local namespace", "UPDATE pre_key SET account_id_type=2 WHERE _id=(SELECT min(_id) " +
				"FROM pre_key)",
			signal.ErrInvalidAccountImport,
		},
		{
			"negative key ID", "UPDATE pre_key SET key_id=-1 WHERE _id=(SELECT min(_id) FROM pre_key)",
			signal.ErrInvalidAccountImport,
		},
		{
			"bad identity address", "UPDATE identity SET address='secret-source-value'",
			signal.ErrInvalidAccountImport,
		},
		{
			"null uuid", "UPDATE identity SET address='00000000-0000-0000-0000-000000000000'",
			signal.ErrInvalidAccountImport,
		},
		{
			"bad trust enum", "UPDATE identity SET trust_level=3",
			signal.ErrInvalidAccountImport,
		},
		{
			"bad bool", "UPDATE kyber_pre_key SET is_last_resort=2",
			signal.ErrInvalidAccountImport,
		},
		{
			"bad session device", "UPDATE session SET device_id=0",
			signal.ErrInvalidAccountImport,
		},
		{
			"bad distribution", "UPDATE sender_key SET distribution_id=X'00'",
			signal.ErrInvalidAccountImport,
		},
		{
			"oversize protocol blob", "UPDATE session SET record=zeroblob(1048577)",
			signal.ErrInvalidAccountImport,
		},
		{
			"empty protocol blob", "UPDATE session SET record=X''",
			signal.ErrInvalidAccountImport,
		},
		{"semantic formatting", "PRAGMA writable_schema=ON; UPDATE sqlite_master SET " +
			"sql=replace(sql,'CREATE TABLE','create  table') WHERE " +
			"type='table'; PRAGMA schema_version=100", nil},
		{
			"foreign key orphan", "INSERT INTO group_v2_member(group_id,recipient_id,endorsement) " +
				"VALUES(99,99,X'00')",
			signal.ErrInvalidAccountImport,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			tree := newSQLSnapshotTree(t)
			if testCase.sql != "" {
				editSQLFixture(t, tree, testCase.sql)
			}

			validateSQLFixture(t, tree, tree.testOptions(), testCase.want)
		})
	}

	t.Run("corrupt database", func(t *testing.T) {
		t.Parallel()
		tree := newSQLSnapshotTree(t)
		check(t, os.WriteFile(tree.database, []byte("secret-source-value"), 0o600))
		validateSQLFixture(t, tree, tree.testOptions(),
			signal.ErrInvalidAccountImport)
	})
	t.Run("lowered row limit", func(t *testing.T) {
		t.Parallel()
		tree := newSQLSnapshotTree(t)
		opts := tree.testOptions()
		opts.ProtocolRowLimit = 1
		validateSQLFixture(t, tree, opts,
			signal.ErrInvalidAccountImport)
	})
}

//nolint:funlen // Keep each related source-contract matrix together.
func TestAccountImportPendingWork(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, sql, cache string
		want             error
	}{
		{"explicit false", "", "", nil},
		{
			"retry absent", "DELETE FROM key_value", "",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"retry true", "UPDATE key_value SET value=1", "",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"retry null", "UPDATE key_value SET value=NULL", "",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"retry text", "UPDATE key_value SET value='0'", "",
			signal.ErrInvalidAccountImport,
		},
		{
			"retry blob", "UPDATE key_value SET value=X'00'", "",
			signal.ErrInvalidAccountImport,
		},
		{
			"retry nonboolean", "UPDATE key_value SET value=2", "",
			signal.ErrInvalidAccountImport,
		},
		{
			"cached message", "", "msg-cache/peer/message",
			signal.ErrUnsupportedAccountImport,
		},
		{"empty cache directories", "", "msg-cache/peer/", nil},
		{
			"content log", "INSERT INTO message_send_log_content(_id,timestamp,content," +
				"content_hint,urgent) VALUES(1,0,X'00',0,0)", "",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"recipient log", "INSERT INTO message_send_log(content_id,address,device_id) " +
				"VALUES(1,'secret-source-value',2)", "",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"excluded outbound sharing", "INSERT INTO sender_key_shared(address,device_id,distribution_id," +
				"timestamp) VALUES('22222222-2222-4222-8222-222222222222',2," +
				"X'66666666666646668666666666666666',0)", "", nil,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			tree := newSQLSnapshotTree(t)
			if testCase.sql != "" {
				editSQLFixture(t, tree, testCase.sql)
			}

			if testCase.cache != "" {
				path := filepath.Join(filepath.Dir(tree.database), testCase.cache)
				if strings.HasSuffix(testCase.cache, "/") {
					check(t, os.MkdirAll(path, 0o700))
				} else {
					check(t, os.MkdirAll(filepath.Dir(path), 0o700))
					check(t, os.WriteFile(path, []byte("secret-source-value"), 0o600))
				}
			}

			validateSQLFixture(t, tree, tree.testOptions(), testCase.want)
		})
	}
}

func TestAccountImportWAL(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{"success", sourceSQLRefusal, sourceSQLCancellation} {
		t.Run(scenario, func(t *testing.T) { t.Parallel(); checkSourceWAL(t, scenario) })
	}
}

func checkSourceWAL(t *testing.T, scenario string) {
	t.Helper()
	tree := newSQLSnapshotTree(t)
	database := openSQLFixture(t, tree.database)
	_, err := database.ExecContext(t.Context(), "PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; INSERT "+
		"INTO key_value(key,value) VALUES('config-read-receipts',0)")
	check(t, err)

	if scenario == sourceSQLRefusal {
		_, err = database.ExecContext(t.Context(), "UPDATE key_value SET value=1 WHERE key='retry-failed-messages'")
		check(t, err)
	}

	wal := must(os.ReadFile(tree.database + "-wal"))(t)
	if len(wal) == 0 {
		t.Fatal("fixture did not retain WAL")
	}

	before := snapshotFiles(t, tree.source)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	opts := tree.testOptions()
	if scenario == sourceSQLCancellation {
		opts.Step = func(step string) {
			if step == "sql:row:identity" {
				cancel()
			}
		}
	}

	snap := must(accountimport.OpenSnapshotForTest(ctx, tree.options(), opts))(t)
	err = snap.ValidateSourceForTest(ctx)

	checkSourceWALResult(t, scenario, snap, err)

	check(t, snap.Close())

	if !reflect.DeepEqual(before, snapshotFiles(t, tree.source)) {
		t.Error("WAL validation changed source")
	}

	if !bytes.Equal(wal, must(os.ReadFile(tree.database+"-wal"))(t)) {
		t.Error("source WAL changed")
	}

	assertNoSnapshotWorkspace(t, tree)
}

//nolint:funlen // Keep each related source-contract matrix together.
func TestAccountImportSourceBounds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, statement string
		options         accountimport.SnapshotTestOptions
		want            error
	}{
		{
			"protocol total exact", "",
			accountimport.SnapshotTestOptions{ProtocolRowLimit: 15},
			nil,
		},
		{
			"protocol total exceeded", "",
			accountimport.SnapshotTestOptions{ProtocolRowLimit: 14},
			signal.ErrInvalidAccountImport,
		},
		{
			"recipients exact", "INSERT INTO recipient(given_name) VALUES('one')",
			accountimport.SnapshotTestOptions{RecipientRowLimit: 1},
			nil,
		},
		{
			"recipients exceeded", "INSERT INTO recipient(given_name) VALUES('one'),('two')",
			accountimport.SnapshotTestOptions{RecipientRowLimit: 1},
			signal.ErrInvalidAccountImport,
		},
		{
			"groups exact", sourceGroup,
			accountimport.SnapshotTestOptions{GroupRowLimit: 1},
			nil,
		},
		{
			"groups exceeded", sourceGroup + "INSERT INTO group_v2(group_id,master_key,distribution_id," +
				"profile_sharing) VALUES(randomblob(32),zeroblob(32)," +
				"randomblob(16),1)",
			accountimport.SnapshotTestOptions{GroupRowLimit: 1},
			signal.ErrInvalidAccountImport,
		},
		{
			"protocol blob exact", "UPDATE session SET record=zeroblob(1048576)",
			accountimport.SnapshotTestOptions{},
			nil,
		},
		{
			"lowered blob bound", "",
			accountimport.SnapshotTestOptions{BlobLimit: 32},
			signal.ErrInvalidAccountImport,
		},
		{
			"schema SQL bound", "PRAGMA writable_schema=ON; UPDATE sqlite_master SET sql=sql||' " +
				"/*'||hex(zeroblob(40000))||'*/' WHERE name='identity'; PRAGMA " +
				"schema_version=100",
			accountimport.SnapshotTestOptions{},
			signal.ErrInvalidAccountImport,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			tree := newSQLSnapshotTree(t)
			if testCase.statement != "" {
				editSQLFixture(t, tree, testCase.statement)
			}

			testCase.options.TempDir = tree.workspace
			validateSQLFixture(t, tree, testCase.options, testCase.want)
		})
	}
}

//nolint:funlen // Keep each related source-contract matrix together.
func TestAccountImportSourceSchema(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, statement string
		want            error
	}{
		{"reordered equivalent index", "DROP INDEX msl_content_index;CREATE INDEX " +
			"equivalent_content_index ON message_send_log(content_id)", nil},
		{
			"expression index", "DROP INDEX msl_content_index;CREATE INDEX msl_content_index ON " +
				"message_send_log(content_id+0)",
			signal.ErrInvalidAccountImport,
		},
		{
			"partial index", "DROP INDEX msl_content_index;CREATE INDEX msl_content_index ON " +
				"message_send_log(content_id) WHERE content_id>0",
			signal.ErrInvalidAccountImport,
		},
		{
			"descending index", "DROP INDEX msl_content_index;CREATE INDEX msl_content_index ON " +
				"message_send_log(content_id DESC)",
			signal.ErrInvalidAccountImport,
		},
		{
			"collated index", "DROP INDEX msl_content_index;CREATE INDEX msl_content_index ON " +
				"message_send_log(content_id COLLATE NOCASE)",
			signal.ErrInvalidAccountImport,
		},
		{
			"extra index", "CREATE INDEX extra_identity_index ON identity(added_timestamp)",
			signal.ErrInvalidAccountImport,
		},
		{"equivalent boolean default", "PRAGMA writable_schema=ON;UPDATE sqlite_master SET " +
			"sql=replace(sql,'DEFAULT FALSE','DEFAULT (0)') WHERE " +
			"name='recipient';PRAGMA schema_version=100", nil},
		{
			"changed default", "PRAGMA writable_schema=ON;UPDATE sqlite_master SET " +
				"sql=replace(sql,'DEFAULT FALSE','DEFAULT TRUE') WHERE " +
				"name='recipient';PRAGMA schema_version=100",
			signal.ErrInvalidAccountImport,
		},
		{
			"removed strict", "PRAGMA writable_schema=ON;UPDATE sqlite_master SET " +
				"sql=replace(sql,') STRICT',')') WHERE name='recipient';PRAGMA " +
				"schema_version=100",
			signal.ErrInvalidAccountImport,
		},
		{
			"deferred FK", "PRAGMA writable_schema=ON;UPDATE sqlite_master SET " +
				"sql=replace(sql,'ON DELETE CASCADE','ON DELETE CASCADE " +
				"DEFERRABLE INITIALLY DEFERRED') WHERE " +
				"name='group_v2_member';PRAGMA schema_version=100",
			signal.ErrInvalidAccountImport,
		},
		{
			"changed FK action", "PRAGMA writable_schema=ON;UPDATE sqlite_master SET " +
				"sql=replace(sql,'ON DELETE CASCADE','ON DELETE SET NULL') WHERE " +
				"name='group_v2_member';PRAGMA schema_version=100",
			signal.ErrInvalidAccountImport,
		},
		{
			"case duplicate identity", "INSERT INTO identity(address,identity_key,added_timestamp," +
				"trust_level) VALUES('abcdefab-abcd-4abc-8abc-abcdefabcdef',X'00'," +
				"0,0),('ABCDEFAB-ABCD-4ABC-8ABC-ABCDEFABCDEF',X'00',0,0)",
			signal.ErrInvalidAccountImport,
		},
		{
			"case duplicate session", "INSERT INTO session(account_id_type,address,device_id,record) " +
				"VALUES(0,'abcdefab-abcd-4abc-8abc-abcdefabcdef',2,X'00'),(0," +
				"'ABCDEFAB-ABCD-4ABC-8ABC-ABCDEFABCDEF',2,X'00')",
			signal.ErrInvalidAccountImport,
		},
		{"same uuid distinct namespaces", "INSERT INTO identity(address,identity_key,added_timestamp," +
			"trust_level) VALUES('abcdefab-abcd-4abc-8abc-abcdefabcdef',X'00'," +
			"0,0),('PNI:abcdefab-abcd-4abc-8abc-abcdefabcdef',X'00',0,0)", nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			tree := newSQLSnapshotTree(t)
			editSQLFixture(t, tree, testCase.statement)
			validateSQLFixture(t, tree, tree.testOptions(), testCase.want)
		})
	}
}

func checkSourceWALResult(t *testing.T, scenario string, snap *accountimport.SnapshotForTest, err error) {
	t.Helper()

	switch scenario {
	case "success":
		check(t, err)

		cfg := must(snap.SourceConfigForTest(t.Context()))(t)
		if cfg.ReadReceipts == nil || *cfg.ReadReceipts {
			t.Error("committed WAL row was not read")
		}
	case sourceSQLRefusal:
		if !errors.Is(err,
			signal.ErrUnsupportedAccountImport) {
			t.Errorf("refusal=%v", err)
		}
	case sourceSQLCancellation:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("cancel=%v", err)
		}
	}
}

func TestAccountImportSourceCopyBinding(t *testing.T) {
	t.Parallel()

	for _, mutation := range []string{
		"workspace", "database symlink", "database hardlink", "WAL symlink", "SHM hardlink",
	} {
		t.Run(mutation, func(t *testing.T) { t.Parallel(); checkSourceCopyBinding(t, mutation) })
	}
}

func checkSourceCopyBinding(t *testing.T, mutation string) {
	t.Helper()
	tree := newSQLSnapshotTree(t)

	snap := must(accountimport.OpenSnapshotForTest(t.Context(), tree.options(), tree.testOptions()))(t)
	defer func() { check(t, snap.Close()) }()

	switch mutation {
	case "workspace":
		check(t, os.Rename(snap.Workspace, snap.Workspace+"-held"))
		check(t, os.Symlink(filepath.Dir(tree.database), snap.Workspace))
	case "database symlink":
		check(t, os.Remove(snap.Database))
		check(t, os.Symlink(tree.database, snap.Database))
	case "database hardlink":
		check(t, os.Remove(snap.Database))
		check(t, os.Link(tree.database, snap.Database))
	case "WAL symlink":
		check(t, os.Symlink(tree.database, snap.Database+"-wal"))
	case "SHM hardlink":
		check(t, os.Link(tree.database, snap.Database+"-shm"))
	}

	before := snapshotFiles(t, tree.source)

	err := snap.ValidateSourceForTest(t.Context())
	if !errors.Is(err, signal.ErrAccountImportSourceChanged) {
		t.Errorf("rebound copy=%v", err)
	}

	if !reflect.DeepEqual(before, snapshotFiles(t, tree.source)) {
		t.Error("SQL opened rebound source directory")
	}
}
