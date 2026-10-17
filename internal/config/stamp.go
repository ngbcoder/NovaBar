package config

import (
	"os"
	"time"
)

// fileStamp returns a cheap identity for a file's current content, combining size and
// modification time.
//
// Used to detect changes across a rename-based save, where an fsnotify watch on the
// original inode stops firing. Returns "" when the file does not exist.
func fileStamp(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	// Nanosecond resolution matters: a rapid double-save can keep the same size.
	return info.ModTime().UTC().Format(time.RFC3339Nano) + "|" + itoa(info.Size())
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
