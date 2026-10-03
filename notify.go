package implugin

import (
	"github.com/daqing/airway-im-plugin/install/lib/im/app/notify"
)

// NotifyUsers queues a host-domain notification for the given users,
// delivered over the IM WebSocket gateway (in-process variant of
// POST /internal/v1/notify). The event name is free-form (conventionally
// prefixed "host."), data any JSON value. Delivery is advisory — online
// recipients receive it in real time; offline recipients are not replayed,
// so pair notifications with a pull endpoint. Requires the framework
// database to be set up (any point after boot).
func NotifyUsers(userUUIDs []string, event string, data any) (eventID string, err error) {
	return notify.Emit(userUUIDs, event, data)
}
