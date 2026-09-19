package migrate

import "sync"

var registerOnce sync.Once

// Register loads this plugin's migrations into the schema registry. The
// package self-registers through init so the migrations are compiled into any
// binary that imports it; the plugin root package (plugin.go) blank-imports
// this package, so a host that enables the plugin gets the migrations in its
// binary.
func Register() {
	registerOnce.Do(func() {
		RegisterUsers()
		RegisterIMCore()
		RegisterMessageModeration()
		RegisterUserTokenVersion()
	})
}

func init() { Register() }
