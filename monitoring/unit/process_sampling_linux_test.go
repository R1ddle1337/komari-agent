package monitoring

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestProcessCountChunks(t *testing.T) {
	root := t.TempDir()
	for i := 1; i <= 600; i++ {
		if err := os.Mkdir(filepath.Join(root, strconv.Itoa(i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"net", "123suffix", "self", "+12", "-1"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	previous := flags.HostProc
	flags.HostProc = root
	defer func() { flags.HostProc = previous }()
	if got := ProcessCount(); got != 600 {
		t.Fatalf("process count = %d", got)
	}
}

func TestMemorySamplingPreservesHostProcIncludeCache(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "meminfo"), []byte("MemTotal: 65536 kB\nMemFree: 16384 kB\nMemAvailable: 32768 kB\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOST_PROC", root)
	previous := flags.MemoryIncludeCache
	flags.MemoryIncludeCache = true
	defer func() { flags.MemoryIncludeCache = previous }()
	ram, _ := MemoryAndSwap()
	if ram.Total != 65536*1024 || ram.Used != (65536-16384)*1024 || ram.Mode != "includeCache" {
		t.Fatalf("HOST_PROC memory source changed: %+v", ram)
	}
}
