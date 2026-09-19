// Package implugin implements the im Airway plugin: a self-contained IM
// backend (identity, conversations, messages, moderation, outbox) that host
// applications enable with a blank import.
package implugin

import (
	"github.com/daqing/airway/lib/plugin"
	"github.com/gin-gonic/gin"

	"github.com/daqing/airway-im-plugin/install/deps/im/app/api/admin_api"
	"github.com/daqing/airway-im-plugin/install/deps/im/app/api/im_api"
	"github.com/daqing/airway-im-plugin/install/deps/im/app/api/me_api"
	"github.com/daqing/airway-im-plugin/install/deps/im/app/models"

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
// it is served by Boot on its own listener (see internal_server.go).
func (Plugin) MountPath() string { return "/" }

func (Plugin) Routes(r *gin.RouterGroup) {
	admin_api.Routes(r)

	v1 := r.Group("/api/v1")
	me_api.Routes(v1)
	im_api.Routes(v1)
}

func (Plugin) REPLModels() map[string]any { return models.REPLModels() }

func init() { plugin.Register(Plugin{}) }
