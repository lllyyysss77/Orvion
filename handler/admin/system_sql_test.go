package admin

import "testing"

func TestNormalizeDatabaseSQLAllowsCRUDStatements(t *testing.T) {
	tests := []struct {
		name  string
		query string
		kind  string
	}{
		{name: "select", query: " SELECT * FROM providers; ", kind: "select"},
		{name: "insert", query: "insert into providers (name) values ('demo')", kind: "insert"},
		{name: "update", query: "UPDATE providers SET name = 'demo' WHERE id = 1", kind: "update"},
		{name: "delete", query: "delete from providers where id = 1", kind: "delete"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, kind, err := normalizeDatabaseSQL(test.query)
			if err != nil {
				t.Fatalf("normalizeDatabaseSQL() error = %v", err)
			}
			if kind != test.kind {
				t.Fatalf("statement type = %q, want %q", kind, test.kind)
			}
		})
	}
}

func TestNormalizeDatabaseSQLRejectsNonCRUDAndMultiStatements(t *testing.T) {
	queries := []string{
		"CREATE TABLE demo (id INTEGER)",
		"DROP TABLE providers",
		"ALTER TABLE providers ADD COLUMN demo TEXT",
		"SELECT 1; DELETE FROM providers",
		"SELECT 1 -- DELETE FROM providers",
		"WITH rows AS (SELECT 1) SELECT * FROM rows",
	}

	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			if _, _, err := normalizeDatabaseSQL(query); err == nil {
				t.Fatal("normalizeDatabaseSQL() expected an error")
			}
		})
	}
}
