package models

import "testing"

func TestREPLModelsIncludesUser(t *testing.T) {
	models := REPLModels()

	model, ok := models["User"]
	if !ok {
		t.Fatalf("expected User in REPL models, got %#v", models)
	}

	if model != (User{}) {
		t.Fatalf("expected zero User, got %#v", model)
	}
}

func TestREPLModelsReturnsACopy(t *testing.T) {
	models := REPLModels()
	delete(models, "User")
	if _, ok := REPLModels()["User"]; !ok {
		t.Fatal("mutating the returned map must not affect the registry")
	}
}

func TestUserTableName(t *testing.T) {
	if name := (User{}).TableName(); name != "users" {
		t.Fatalf("expected table name %q, got %q", "users", name)
	}
}
