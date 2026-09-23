//go:build !windows
// +build !windows

package monitoring

import (
	"io"
	"os"
)

// ProcessCount returns the number of running processes
func ProcessCount() (count int) {
	return processCountLinux()
}

// processCountLinux counts processes by reading /proc directory
func processCountLinux() (count int) {
	procDir := "/proc"

	if flags.HostProc != "" {
		if info, err := os.Stat(flags.HostProc); err == nil && info.IsDir() {
			procDir = flags.HostProc
		}
	}

	dir, err := os.Open(procDir)
	if err != nil {
		return 0
	}
	defer dir.Close()
	// Only names are needed. Read in bounded chunks without DirEntry creation,
	// metadata reads, integer parsing errors, or sorting every process.
	for {
		names, err := dir.Readdirnames(256)
		for _, name := range names {
			if isProcessID(name) {
				count++
			}
		}
		if err == io.EOF {
			return count
		}
		if err != nil {
			return 0
		}
	}
}

func isProcessID(name string) bool {
	if name == "" {
		return false
	}
	for i := range name {
		if name[i] < '0' || name[i] > '9' {
			return false
		}
	}
	return true
}
