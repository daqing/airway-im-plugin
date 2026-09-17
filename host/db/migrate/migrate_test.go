package migrate

import (
	"testing"

	"github.com/daqing/airway/lib/migrate/schema"
)

func definitionsByVersion() map[string]schema.Definition {
	defs := make(map[string]schema.Definition)
	for _, def := range schema.Definitions() {
		defs[def.Version] = def
	}
	return defs
}

func TestAllMigrationsAreRegistered(t *testing.T) {
	want := map[string]string{
		"20260721000000": "create_im_core",
		"20260724000000": "add_message_moderation",
		"20260914000000": "create_users",
		"20260915000000": "add_user_token_version",
	}
	defs := definitionsByVersion()
	for version, name := range want {
		def, ok := defs[version]
		if !ok {
			t.Fatalf("migration %s (%s) is not registered", version, name)
		}
		if def.Name != name {
			t.Fatalf("migration %s named %q, want %q", version, def.Name, name)
		}
		if len(def.UpOps) == 0 {
			t.Fatalf("migration %s has no up operations", version)
		}
		if len(def.DownOps) == 0 {
			t.Fatalf("migration %s has no reversible down operations", version)
		}
	}
}

func TestRegisterIsIdempotent(t *testing.T) {
	// A second Register must not re-register (and panic on duplicate
	// versions): the package guards itself with sync.Once.
	Register()
	Register()
}

func TestTokenVersionMigrationShape(t *testing.T) {
	def, ok := definitionsByVersion()["20260915000000"]
	if !ok {
		t.Fatal("add_user_token_version is not registered")
	}
	if len(def.UpOps) != 1 {
		t.Fatalf("expected exactly one operation, got %d", len(def.UpOps))
	}
	add, ok := def.UpOps[0].(schema.AddColumnOp)
	if !ok {
		t.Fatalf("operation is %T, want schema.AddColumnOp", def.UpOps[0])
	}
	if add.Table != "users" || add.Column.Name != "token_version" {
		t.Fatalf("adds %s.%s, want users.token_version", add.Table, add.Column.Name)
	}
	if add.Column.Type.Kind != schema.TypeBigInt {
		t.Fatalf("column type = %q, want bigint", add.Column.Type.Kind)
	}
	if add.Column.Null == nil || *add.Column.Null {
		t.Fatal("column must be NOT NULL")
	}
	if add.Column.Default != 1 {
		t.Fatalf("column default = %v, want 1", add.Column.Default)
	}
}
