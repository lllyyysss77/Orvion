package models

import (
	"net/url"
	"strings"
	"testing"
)

func TestConfigureSQLiteDSNAddsLockPragmas(t *testing.T) {
	dsn := configureSQLiteDSN("data/llmio.db")
	if !strings.HasPrefix(dsn, "data/llmio.db?") {
		t.Fatalf("dsn path changed: %s", dsn)
	}

	pragmas := sqlitePragmasForTest(t, dsn)
	for _, want := range []string{
		"busy_timeout(30000)",
		"foreign_keys(1)",
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
	} {
		if !containsString(pragmas, want) {
			t.Fatalf("missing pragma %q in %v", want, pragmas)
		}
	}
}

func TestConfigureSQLiteDSNRespectsExistingPragmas(t *testing.T) {
	dsn := configureSQLiteDSN("file:data/llmio.db?cache=shared&_pragma=busy_timeout(7000)&_pragma=journal_mode(DELETE)")
	values, err := url.ParseQuery(sqliteQueryString(dsn))
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	if got := values.Get("cache"); got != "shared" {
		t.Fatalf("cache param changed: %q", got)
	}

	pragmas := values["_pragma"]
	if !containsString(pragmas, "busy_timeout(7000)") || containsString(pragmas, "busy_timeout(30000)") {
		t.Fatalf("busy_timeout should keep user value: %v", pragmas)
	}
	if !containsString(pragmas, "journal_mode(DELETE)") || containsString(pragmas, "journal_mode(WAL)") {
		t.Fatalf("journal_mode should keep user value: %v", pragmas)
	}
	if !containsString(pragmas, "foreign_keys(1)") || !containsString(pragmas, "synchronous(NORMAL)") {
		t.Fatalf("missing default non-conflicting pragmas: %v", pragmas)
	}
}

func TestConfigureSQLiteDSNSkipsWALForMemory(t *testing.T) {
	dsn := configureSQLiteDSN("file:test?mode=memory&cache=shared")
	pragmas := sqlitePragmasForTest(t, dsn)
	if containsString(pragmas, "journal_mode(WAL)") {
		t.Fatalf("memory sqlite should not enable WAL: %v", pragmas)
	}
	if !containsString(pragmas, "busy_timeout(30000)") || !containsString(pragmas, "foreign_keys(1)") {
		t.Fatalf("memory sqlite should keep connection pragmas: %v", pragmas)
	}
}

func TestNormalizeDatabaseDriverDetectsMySQLScheme(t *testing.T) {
	if got := normalizeDatabaseDriver("", "mysql://user:pass@127.0.0.1:3306/orvion"); got != string(DatabaseDriverMySQL) {
		t.Fatalf("driver = %q, want mysql", got)
	}
	if got := normalizeDatabaseDriver("mysql", "data/llmio.db"); got != string(DatabaseDriverMySQL) {
		t.Fatalf("explicit driver = %q, want mysql", got)
	}
	if got := normalizeDatabaseDriver("", "data/llmio.db"); got != string(DatabaseDriverSQLite) {
		t.Fatalf("sqlite path driver = %q, want sqlite", got)
	}
}

func TestNormalizeMySQLDSNFromURL(t *testing.T) {
	dsn, err := normalizeMySQLDSN("mysql://orvion:secret@127.0.0.1:3306/orvion")
	if err != nil {
		t.Fatalf("normalize mysql url: %v", err)
	}
	want := "orvion:secret@tcp(127.0.0.1:3306)/orvion?charset=utf8mb4&loc=Local&parseTime=true"
	if dsn != want {
		t.Fatalf("dsn = %q, want %q", dsn, want)
	}
}

func TestNormalizeMySQLDSNAddsDefaultsToRawDSN(t *testing.T) {
	dsn, err := normalizeMySQLDSN("orvion:secret@tcp(localhost:3306)/orvion")
	if err != nil {
		t.Fatalf("normalize raw mysql dsn: %v", err)
	}
	want := "orvion:secret@tcp(localhost:3306)/orvion?charset=utf8mb4&loc=Local&parseTime=true"
	if dsn != want {
		t.Fatalf("dsn = %q, want %q", dsn, want)
	}
}

func sqlitePragmasForTest(t *testing.T, dsn string) []string {
	t.Helper()
	values, err := url.ParseQuery(sqliteQueryString(dsn))
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	return values["_pragma"]
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
