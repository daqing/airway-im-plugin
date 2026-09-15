package config

import (
	"github.com/gin-gonic/gin"

	"github.com/daqing/airway-im-plugin/app/api/internal_api"
	"github.com/daqing/airway-im-plugin/deps/backend/app/api/health_api"
	"github.com/daqing/airway-im-plugin/deps/backend/app/api/home_api"
	"github.com/daqing/airway-im-plugin/deps/backend/app/api/storage_api"
	"github.com/daqing/airway/lib/plugin"

	// Register the IM plugin so MountAll below mounts its routes.
	_ "github.com/daqing/airway-im-plugin"
)

// Routes registers every route — public and internal — at the root paths. This
// is the full router used when the app is served without a URL_PREFIX. The
// plugin's internal API is already mounted by PublicRoutes via MountAll, so
// only the health check is added on top.
func Routes(r *gin.Engine) {
	PublicRoutes(r)
	health_api.Routes(r)
}

// PublicRoutes registers the user-facing routes: the home page, the storage
// API, and every enabled plugin (the IM API and the admin API come from the
// im plugin). When a URL_PREFIX is configured these answer only under the
// prefix; see App.Handler.
func PublicRoutes(r *gin.Engine) {
	r.GET("/", home_api.IndexAction)

	v1 := r.Group("/api/v1")
	storage_api.Routes(v1)

	plugin.MountAll(r)
}

// HealthRoutes registers the routes that stay reachable at the unprefixed
// root (for load-balancer probes) even when the public routes are served
// under a URL_PREFIX: the health check plus the service-to-service internal
// API that the gateway and delivery services call on the backend directly.
func HealthRoutes(r *gin.Engine) {
	health_api.Routes(r)
	internal_api.Routes(r.Group("/"))
}
