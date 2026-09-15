package implugin

import (
	_ "embed"
	"strings"
)

// version is read from the VERSION file at the module root and compiled into
// the binary.
//
//go:embed VERSION
var version string

// Version returns the plugin module version.
func Version() string { return strings.TrimSpace(version) }
