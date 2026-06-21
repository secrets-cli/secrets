//go:build !unix

package vault

// lockStore is a no-op where flock is unavailable. vars targets Unix
// (Linux/macOS/WSL); native Windows is out of scope, so mutations there are not
// serialized across processes.
func lockStore(dir string) (func(), error) { return func() {}, nil }
