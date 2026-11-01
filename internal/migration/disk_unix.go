//go:build !windows

package migration

import "syscall"

func availableBytes(path string) (uint64, error) {
	var disk syscall.Statfs_t
	if err := syscall.Statfs(path, &disk); err != nil {
		return 0, err
	}
	return uint64(disk.Bavail) * uint64(disk.Bsize), nil
}
