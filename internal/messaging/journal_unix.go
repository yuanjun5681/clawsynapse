//go:build !windows

package messaging

import (
	"os"
	"syscall"
)

// lockJournal takes a non-blocking exclusive advisory lock so a second daemon
// sharing the same state file fails fast instead of interleaving writes.
// The lock is released when the file is closed.
func lockJournal(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
