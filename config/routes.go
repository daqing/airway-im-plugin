package config

import (
	"github.com/gin-gonic/gin"

	"github.com/daqing/airway-im-plugin/app/api/admin_api"
	"github.com/daqing/airway-im-plugin/app/api/health_api"
	"github.com/daqing/airway-im-plugin/app/api/home_api"
	"github.com/daqing/airway-im-plugin/app/api/im_api"
	"github.com/daqing/airway-im-plugin/app/api/internal_api"
	"github.com/daqing/airway-im-plugin/app/api/me_api"
	"github.com/daqing/airway-im-plugin/app/api/storage_api"
	"github.com/daqing/airway/lib/plugin"
)

// Routes registers every route — public and internal — at the root paths. This
// is the full router used when the app is served without a URL_PREFIX.
func Routes(r *gin.Engine) {
	PublicRoutes(r)
	HealthRoutes(r)
}

// PublicRoutes registers the user-facing routes: the home page, the admin
// API, and the IM API. When a URL_PREFIX is configured these answer only
// under the prefix; see App.Handler.
func PublicRoutes(r *gin.Engine) {
	r.GET("/", home_api.IndexAction)

	admin_api.Routes(r)

	apiGroupRoutes(r)

	plugin.MountAll(r)
}

// HealthRoutes registers the routes that stay reachable at the unprefixed
// root (for load-balancer probes) even when the public routes are served
// under a URL_PREFIX: the health check plus the service-to-service internal
// API that the gateway and delivery services call on the backend directly.
func HealthRoutes(r *gin.Engine) {
	health_api.Routes(r)
	internal_api.Routes(r)
}

func apiGroupRoutes(r *gin.Engine) {
	v1 := r.Group("/api/v1")
	{
		me_api.Routes(v1)
		im_api.Routes(v1)
		storage_api.Routes(v1)
	}
}
