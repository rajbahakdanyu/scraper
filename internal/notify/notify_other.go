//go:build !darwin && !linux

package notify

func send(string) error { return nil }
