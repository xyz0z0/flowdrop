//go:build windows

package storage

import "golang.org/x/sys/windows"

// availableBytes reports the bytes free on the volume holding dir, as seen by
// the caller (FreeBytesAvailableToCaller matches the Unix Bavail notion).
func availableBytes(dir string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var freeToCaller, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeToCaller, &total, &totalFree); err != nil {
		return 0, err
	}
	return freeToCaller, nil
}
