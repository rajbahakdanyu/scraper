// Package notify shows a desktop notification when a run finishes.
package notify

// Send shows summary as a desktop notification. It is a no-op for an empty
// summary and on systems without a notification service.
func Send(summary string) error {
	if summary == "" {
		return nil
	}
	return send(summary)
}
