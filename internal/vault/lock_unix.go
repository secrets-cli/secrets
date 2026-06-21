//go:build unix

package vault

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockStore takes an exclusive advisory lock on the store so concurrent vars
// mutations serialize instead of racing the git index or each other's renames.
// It returns a function that releases the lock. The lock is advisory (only other
// vars processes honor it), held on the gitignored .vars.lock file, and released
// by the kernel if the process dies (so there is no stale lock to clean up).
func lockStore(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_CREATE|os.O_RDWR, filePerm)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
