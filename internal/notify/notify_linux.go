package notify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	notificationsName  = "org.freedesktop.Notifications"
	notificationsPath  = "/org/freedesktop/Notifications"
	notificationsCall  = notificationsName + ".Notify"
	notifyCallDeadline = 5 * time.Second
)

// send calls the freedesktop notification service directly over the
// session bus. Without a desktop session (SSH, headless) there is nobody to
// notify, so that is not an error.
func send(summary string) error {
	// NoAutoStartup: never spawn a dbus-daemon just to show a notification.
	conn, err := dbus.SessionBusPrivateNoAutoStartup()
	if err != nil {
		return nil
	}
	defer conn.Close()
	if err := conn.Auth(nil); err != nil {
		return fmt.Errorf("notification: d-bus auth: %w", err)
	}
	if err := conn.Hello(); err != nil {
		return fmt.Errorf("notification: d-bus hello: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), notifyCallDeadline)
	defer cancel()
	err = conn.Object(notificationsName, notificationsPath).CallWithContext(ctx, notificationsCall, 0,
		"scraper",                 // app_name
		uint32(0),                 // replaces_id
		"",                        // app_icon
		"scraper",                 // summary (title)
		summary,                   // body
		[]string{},                // actions
		map[string]dbus.Variant{}, // hints
		int32(-1),                 // expire_timeout: server default
	).Err
	var dbusErr dbus.Error
	if errors.As(err, &dbusErr) && dbusErr.Name == "org.freedesktop.DBus.Error.ServiceUnknown" {
		return nil // session without a notification daemon
	}
	if err != nil {
		return fmt.Errorf("notification: %w", err)
	}
	return nil
}
