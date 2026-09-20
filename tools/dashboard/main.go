// Command dashboard bundles the IM admin console frontend (web/) into the
// committed embed directory (web/dist) with esbuild — the same no-Node
// pipeline the airway CLI uses for host frontends, pointed at the plugin's
// own sources. Run from the plugin repository root:
//
//	go run ./tools/dashboard
//
// The build writes app.js (plus app.css and sourcemaps when emitted) and a
// manifest.json whose hash the Go side uses as the ?v= cache buster. Output
// is committed, so hosts never need to run this.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

const (
	webDir = "install/lib/im/app/dashboard/web"
	entry  = "app.tsx"
)

// alias maps react imports onto preact/compat, matching lib/jsbuild so the
// React-ecosystem libraries (TanStack Table/Query) bundle against Preact.
func alias() map[string]string {
	return map[string]string{
		"react":                 "preact/compat",
		"react/jsx-runtime":     "preact/compat/jsx-runtime",
		"react/jsx-dev-runtime": "preact/compat/jsx-runtime",
		"react-dom":             "preact/compat",
		"react-dom/client":      "preact/compat/client",
		"react-dom/server":      "preact/compat/server",
	}
}

type manifest struct {
	Entry string   `json:"entry"`
	Hash  string   `json:"hash"`
	Files []string `json:"files"`
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		fail("resolve working directory: %v", err)
	}
	web := filepath.Join(root, webDir)
	dist := filepath.Join(web, "dist")

	if info, err := os.Stat(filepath.Join(web, "vendor")); err != nil || !info.IsDir() {
		fail("%s is missing; the console's vendored npm packages are not installed", filepath.Join(webDir, "vendor"))
	}

	result := api.Build(api.BuildOptions{
		EntryPoints:       []string{filepath.Join(web, entry)},
		Outdir:            dist,
		Bundle:            true,
		Write:             true,
		Format:            api.FormatESModule,
		Target:            api.ES2020,
		Platform:          api.PlatformBrowser,
		JSX:               api.JSXAutomatic,
		JSXImportSource:   "react",
		Alias:             alias(),
		NodePaths:         []string{filepath.Join(web, "vendor")},
		Define:            map[string]string{"process.env.NODE_ENV": `"production"`},
		MinifyWhitespace:  true,
		MinifySyntax:      true,
		MinifyIdentifiers: true,
		Sourcemap:         api.SourceMapExternal,
		AbsWorkingDir:     root,
		LogLevel:          api.LogLevelWarning,
		LogOverride:       map[string]api.LogLevel{"ignored-bare-import": api.LogLevelSilent},
	})
	if len(result.Errors) > 0 {
		for _, e := range result.Errors {
			fmt.Fprintf(os.Stderr, "%s\n", e.Text)
		}
		os.Exit(1)
	}

	bundle, err := os.ReadFile(filepath.Join(dist, "app.js"))
	if err != nil {
		fail("read bundle: %v", err)
	}
	digest := sha256.Sum256(bundle)
	hash := hex.EncodeToString(digest[:8])

	files := []string{}
	for _, outputFile := range result.OutputFiles {
		files = append(files, filepath.Base(outputFile.Path))
	}
	sort.Strings(files)

	encoded, err := json.MarshalIndent(manifest{Entry: "app.js", Hash: hash, Files: files}, "", "  ")
	if err != nil {
		fail("encode manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dist, "manifest.json"), append(encoded, '\n'), 0o644); err != nil {
		fail("write manifest: %v", err)
	}

	fmt.Printf("dashboard: built %s (hash %s)\n", strings.Join(files, ", "), hash)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "dashboard: "+format+"\n", args...)
	os.Exit(1)
}
