//go:build windows

package messaging

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockJournal takes a non-blocking exclusive byte-range lock on the journal,
// the Windows equivalent of flock LOCK_EX|LOCK_NB. The lock is released when
// the handle is closed.
func lockJournal(f *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
}
