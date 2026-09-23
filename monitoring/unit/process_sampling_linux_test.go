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
