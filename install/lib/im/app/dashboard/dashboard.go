// Package dashboard serves the IM admin web console: an embedded Preact
// bundle (TanStack Query + TanStack Table on a vendored copy of the
// airway-ui component set) that talks to the plugin's admin_api HTTP
// surface. The bundle is committed under web/dist and built with
// `go run ./tools/dashboard`; hosts never rebuild it.
package dashboard

import (
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/daqing/airway/lib/utils"
	"github.com/gin-gonic/gin"
)

//go:embed web/dist
var dist embed.FS

type manifest struct {
	Entry string   `json:"entry"`
	Hash  string   `json:"hash"`
	Files []string `json:"files"`
}

// loadManifest reads the build manifest once; the embedded dist never
// changes during a process's lifetime.
func loadManifest() (*manifest, error) {
	data, err := dist.ReadFile("web/dist/manifest.json")
	if err != nil {
		return nil, fmt.Errorf("dashboard: embedded manifest missing: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("dashboard: embedded manifest invalid: %w", err)
	}
	if m.Hash == "" || len(m.Files) == 0 {
		return nil, fmt.Errorf("dashboard: embedded manifest incomplete")
	}
	return &m, nil
}

var cached = sync.OnceValues(loadManifest)

// Routes registers the console under /admin/im, next to the admin API's
// /admin/api: the HTML shell at the index and the embedded bundle below
// /admin/im/assets/.
func Routes(r *gin.RouterGroup) {
	r.GET("/admin/im", func(c *gin.Context) {
		m, err := cached()
		if err != nil {
			c.String(http.StatusInternalServerError, "%v", err)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(page(c, m)))
	})
	r.GET("/admin/im/assets/:file", func(c *gin.Context) {
		m, err := cached()
		if err != nil {
			c.String(http.StatusInternalServerError, "%v", err)
			return
		}
		serveAsset(c, m)
	})
}

// page renders the HTML shell. The mount-path prefix is injected as
// window.IM_ADMIN.base so the bundle can reach both its own assets and the
// admin API when the host serves under a URL_PREFIX sub-path.
func page(c *gin.Context, m *manifest) string {
	prefix := html.EscapeString(utils.URLPrefix())
	version := html.EscapeString(m.Hash)
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<meta name="robots" content="noindex"/>
<title>IM Admin</title>
<link rel="stylesheet" href="%s/admin/im/assets/app.css?v=%s"/>
<script type="module" src="%s/admin/im/assets/app.js?v=%s"></script>
</head>
<body>
<div id="im-admin-root"></div>
<script>window.IM_ADMIN = { base: "%s" };</script>
</body>
</html>
`, prefix, version, prefix, version, prefix)
}

func serveAsset(c *gin.Context, m *manifest) {
	file := path.Clean("/" + c.Param("file"))[1:]
	if file == "" || strings.Contains(file, "..") || !m.contains(file) {
		c.Status(http.StatusNotFound)
		return
	}
	data, err := dist.ReadFile("web/dist/" + file)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}

	contentType := mime.TypeByExtension(path.Ext(file))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if strings.HasPrefix(contentType, "text/javascript") {
		contentType = "text/javascript; charset=utf-8"
	}
	if strings.HasPrefix(contentType, "text/css") {
		contentType = "text/css; charset=utf-8"
	}

	// The bundle URL carries ?v=<hash>: a matching request is immutable,
	// anything else (stale caches, hash-less links) revalidates.
	if c.Query("v") == m.Hash {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		c.Header("Cache-Control", "no-cache")
	}
	c.Data(http.StatusOK, contentType, data)
}

func (m *manifest) contains(file string) bool {
	for _, name := range m.Files {
		if name == file {
			return true
		}
	}
	return false
}
