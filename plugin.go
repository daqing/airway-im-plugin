// Package implugin implements the im Airway plugin: a self-contained IM
// backend (identity, conversations, messages, moderation, outbox) that host
// applications enable with a blank import. Implementation packages live
// under install/lib/im/app, compiled into the plugin binary; this root
// package is the plugin contract the framework sees.
package implugin

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/daqing/airway/lib/plugin"
	"github.com/gin-gonic/gin"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/api/admin_api"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/api/im_api"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/api/internal_api"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/api/me_api"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/dashboard"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/models"

	// The IM data model's Go DSL migrations self-register on import, so a
	// host's db:migrate / db:rollback see them as soon as the plugin is
	// enabled.
	_ "github.com/daqing/airway-im-plugin/install/host/db/migrate"
)

// Plugin is the IM feature module.
type Plugin struct{}

func (Plugin) Name() string { return "im" }

// MountPath keeps the plugin's existing HTTP surface unchanged: routes are
// registered at their absolute paths (/admin/api, /api/v1/...) so clients
// need no reconfiguration. The internal service-to-service API is not here;
// it is served by Boot on its own listener.
func (Plugin) MountPath() string { return "/" }

func (Plugin) Routes(r *gin.RouterGroup) {
	dashboard.Routes(r)

	admin_api.Routes(r)

	v1 := r.Group("/api/v1")
	me_api.Routes(v1)
	im_api.Routes(v1)
}

// Boot starts the internal service-to-service API on its own listener
// (IM_INTERNAL_ADDR, default 127.0.0.1:1906) so it never shares the public
// HTTP surface. Missing required secrets abort the host boot: the framework
// logs the returned error and exits.
func (Plugin) Boot() error {
	var missing []string
	if strings.TrimSpace(os.Getenv("IM_AUTH_SECRET")) == "" {
		missing = append(missing, "IM_AUTH_SECRET (signs client credentials; generate one with `openssl rand -hex 32`)")
	}
	if strings.TrimSpace(os.Getenv("IM_INTERNAL_SECRET")) == "" {
		missing = append(missing, "IM_INTERNAL_SECRET (protects the internal API; gateway/delivery must share it)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("required environment variable(s) not set: %s; add them to the host's .env and restart (see .env.example)", strings.Join(missing, "; "))
	}

	addr := envOr("IM_INTERNAL_ADDR", "127.0.0.1:1906")

	r := gin.New()
	internal_api.Routes(r.Group("/"))

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go func() { _ = http.Serve(ln, r) }()
	return nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func (Plugin) REPLModels() map[string]any { return models.REPLModels() }

func init() { plugin.Register(Plugin{}) }
