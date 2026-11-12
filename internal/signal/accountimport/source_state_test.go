//go:build linux && (cgo || libsignal_go)

package accountimport_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/accountimport"
)

const (
	sourceRecipient = "INSERT INTO recipient(aci,pni,number) " +
		"VALUES('22222222-2222-4222-8222-222222222222'," +
		"'PNI:44444444-4444-4444-8444-444444444444','+12025550222');"
	sourceGroup = "INSERT INTO group_v2(group_id,master_key,distribution_id," +
		"profile_sharing) VALUES(zeroblob(32),zeroblob(32)," +
		"X'66666666666646668666666666666666',1);"
)

//nolint:funlen // Keep the supported-state contract in one table.
func TestAccountImportUnsupportedState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, sql string
		want      error
	}{
		{"addressed recipient", sourceRecipient, nil},
		{"addressless supported names", "INSERT INTO recipient(given_name) VALUES('invented name')", nil},
		{"blocked ACI", sourceRecipient + "UPDATE recipient SET blocked=1", nil},
		{
			"blocked PNI only", "INSERT INTO recipient(pni,blocked) " +
				"VALUES('PNI:44444444-4444-4444-8444-444444444444',1)",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"blocked number only", "INSERT INTO recipient(number,blocked) VALUES('+12025550222',1)",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"addressless sharing", "INSERT INTO recipient(profile_sharing) VALUES(1)",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"addressless key", "INSERT INTO recipient(profile_key) VALUES(zeroblob(32))",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"bare PNI", "INSERT INTO recipient(pni) VALUES('44444444-4444-4444-8444-444444444444')",
			signal.ErrInvalidAccountImport,
		},
		{
			"PNI in ACI column", "INSERT INTO recipient(aci) " +
				"VALUES('PNI:44444444-4444-4444-8444-444444444444')",
			signal.ErrInvalidAccountImport,
		},
		{
			"bad number", "INSERT INTO recipient(number) VALUES('secret-source-value')",
			signal.ErrInvalidAccountImport,
		},
		{
			"case duplicate ACI", "INSERT INTO recipient(aci) " +
				"VALUES('abcdefab-abcd-4abc-8abc-abcdefabcdef')," +
				"('ABCDEFAB-ABCD-4ABC-8ABC-ABCDEFABCDEF')",
			signal.ErrInvalidAccountImport,
		},
		{"equal cross namespace UUID", "INSERT INTO recipient(aci,pni) " +
			"VALUES('abcdefab-abcd-4abc-8abc-abcdefabcdef'," +
			"'PNI:abcdefab-abcd-4abc-8abc-abcdefabcdef')", nil},
		{
			"self wrong number", "INSERT INTO recipient(aci,number) " +
				"VALUES('11111111-1111-4111-8111-111111111111','+12025550999')",
			signal.ErrInvalidAccountImport,
		},
		{
			"own number wrong ACI", "INSERT INTO recipient(aci,number) " +
				"VALUES('22222222-2222-4222-8222-222222222222','+12025550111')",
			signal.ErrInvalidAccountImport,
		},
		{
			"self wrong PNI", "INSERT INTO recipient(aci,pni) " +
				"VALUES('11111111-1111-4111-8111-111111111111'," +
				"'PNI:44444444-4444-4444-8444-444444444444')",
			signal.ErrInvalidAccountImport,
		},
		{
			"nonboolean recipient", sourceRecipient + "UPDATE recipient SET discoverable=2",
			signal.ErrInvalidAccountImport,
		},
		{
			"mute", sourceRecipient + "UPDATE recipient SET mute_until=1",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"archive", sourceRecipient + "UPDATE recipient SET archived=1",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"hidden", sourceRecipient + "UPDATE recipient SET hidden=1",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"story", sourceRecipient + "UPDATE recipient SET hide_story=1",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"negative timer", sourceRecipient + "UPDATE recipient SET expiration_time=-1",
			signal.ErrInvalidAccountImport,
		},
		{
			"timer overflow", sourceRecipient + "UPDATE recipient SET expiration_time=4294967296",
			signal.ErrInvalidAccountImport,
		},
		{"timer max", sourceRecipient + "UPDATE recipient SET expiration_time=4294967295," +
			"expiration_time_version=4294967295", nil},
		{
			"number timer", "INSERT INTO recipient(number,expiration_time) VALUES('+12025550222',1)",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"PNI timer", "INSERT INTO recipient(pni,expiration_time) " +
				"VALUES('PNI:44444444-4444-4444-8444-444444444444',1)",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"number explicit timer version", "INSERT INTO recipient(number,expiration_time_version) " +
				"VALUES('+12025550222',2)",
			signal.ErrUnsupportedAccountImport,
		},
		{"valid group", sourceGroup, nil},
		{
			"blocked group", sourceGroup + "UPDATE group_v2 SET blocked=1",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"nonsharing group", sourceGroup + "UPDATE group_v2 SET profile_sharing=0",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"legacy group", "INSERT INTO group_v1(group_id) VALUES(X'00')",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"bad group boolean", sourceGroup + "UPDATE group_v2 SET blocked=2",
			signal.ErrInvalidAccountImport,
		},
		{
			"sharing contacts", "INSERT INTO key_value(key,value) " +
				"VALUES('config-phone-number-sharing-mode','CONTACTS')",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"sharing unknown enum", "INSERT INTO key_value(key,value) " +
				"VALUES('config-phone-number-sharing-mode','secret-source-value')",
			signal.ErrInvalidAccountImport,
		},
		{"sharing nobody", "INSERT INTO key_value(key,value) " +
			"VALUES('config-phone-number-sharing-mode','NOBODY')", nil},
		{"false setting", "INSERT INTO key_value(key,value) VALUES('config-read-receipts',0)", nil},
		{"null setting", "INSERT INTO key_value(key,value) VALUES('config-read-receipts',NULL)", nil},
		{
			"text setting", "INSERT INTO key_value(key,value) VALUES('config-read-receipts','false')",
			signal.ErrInvalidAccountImport,
		},
		{
			"nonboolean setting", "INSERT INTO key_value(key,value) VALUES('config-read-receipts',2)",
			signal.ErrInvalidAccountImport,
		},
		{
			"unknown key", "INSERT INTO key_value(key,value) VALUES('config-secret-source-value',0)",
			signal.ErrUnsupportedAccountImport,
		},
		{
			"manifest negative", "INSERT INTO key_value(key,value) VALUES('storage-manifest-version',-2)",
			signal.ErrInvalidAccountImport,
		},
		{"manifest minus one", "INSERT INTO key_value(key,value) VALUES('storage-manifest-version',-1)", nil},
		{
			"bad profile key", sourceRecipient + "UPDATE recipient SET profile_key=X'00'",
			signal.ErrInvalidAccountImport,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			tree := newSQLSnapshotTree(t)
			editSQLFixture(t, tree, testCase.sql)
			validateSQLFixture(t, tree, tree.testOptions(), testCase.want)
		})
	}
}

func TestAccountImportSourceRows(t *testing.T) {
	t.Parallel()
	tree := newSQLSnapshotTree(t)
	editSQLFixture(t, tree, sourceRecipient+"INSERT INTO key_value(key,value) VALUES('config-read-receipts',"+
		"0),('config-typing-indicators',NULL),('storage-manifest-version',"+
		"42)")
	before := snapshotFiles(t, tree.source)

	snap := must(accountimport.OpenSnapshotForTest(t.Context(), tree.options(), tree.testOptions()))(t)
	defer func() { check(t, snap.Close()) }()

	cfg := must(snap.SourceConfigForTest(t.Context()))(t)
	if cfg.ReadReceipts == nil || *cfg.ReadReceipts || cfg.TypingIndicators != nil {
		t.Fatal("SQL null and false were conflated")
	}

	got := must(snap.SourceRowsForTest(t.Context(), "pre_key"))(t)
	if len(got) != 6 {
		t.Fatalf("prekeys=%d", len(got))
	}

	publicKey, ok := got[0].Values["public_key"].([]byte)
	if !ok || got[0].Values["key_id"] != int64(0) || len(publicKey) != 33 {
		t.Error("row data changed")
	}

	if !reflect.DeepEqual(before, snapshotFiles(t, tree.source)) {
		t.Fatal("reading rows changed source")
	}

	checkSourceTypedRows(t, snap)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := snap.ValidateSourceForTest(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancel=%v", err)
	}
}

func TestAccountImportSourceConnection(t *testing.T) {
	t.Parallel()
	tree := newSQLSnapshotTree(t)
	before := snapshotFiles(t, tree.source)

	snap := must(accountimport.OpenSnapshotForTest(t.Context(), tree.options(), tree.testOptions()))(t)
	defer func() { check(t, snap.Close()) }()

	trusted, queryOnly, writeErr, err := snap.SourceConnectionForTest(t.Context())
	check(t, err)

	if trusted != 0 || queryOnly != 1 || writeErr == nil {
		t.Fatalf("SQL connection lost guards: trusted=%d query-only=%d write-error=%v", trusted, queryOnly, writeErr)
	}

	if !reflect.DeepEqual(before, snapshotFiles(t, tree.source)) {
		t.Error("connection test changed source")
	}
}

func checkSourceTypedRows(t *testing.T, snap *accountimport.SnapshotForTest) {
	t.Helper()

	recipients := must(snap.SourceRowsForTest(t.Context(), "recipient"))(t)
	if recipients[0].ServiceIDs["aci"] != "22222222-2222-4222-8222-222222222222" ||
		recipients[0].ServiceIDs["pni"] != "PNI:44444444-4444-4444-8444-444444444444" {
		t.Fatal("streamed recipient lost typed service IDs")
	}

	sessions := must(snap.SourceRowsForTest(t.Context(), "session"))(t)
	if sessions[0].ServiceIDs["address"] != "22222222-2222-4222-8222-222222222222" {
		t.Fatal("streamed session lost typed service ID")
	}
}
