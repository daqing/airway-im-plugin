package implugin

import (
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/daqing/airway-im-plugin/install/deps/im/app/api/internal_api"
)

// Boot starts the internal service-to-service API on its own listener
// (IM_INTERNAL_ADDR, default 127.0.0.1:1906) so it never shares the public
// HTTP surface. Handlers still require the X-IM-Internal-Secret header.
func (Plugin) Boot() error {
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
