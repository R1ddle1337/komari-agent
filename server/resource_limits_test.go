package server

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestControlResponseLimit(t *testing.T) {
	for _, size := range []int{0, 128, maxControlResponseBytes, maxControlResponseBytes + 1} {
		data, err := readControlResponse(strings.NewReader(strings.Repeat("x", size)))
		if size > maxControlResponseBytes {
			if !errors.Is(err, errControlResponseTooLarge) || data != nil {
				t.Fatal("oversized control response accepted")
			}
		} else if err != nil || len(data) != size {
			t.Fatalf("valid response size %d: %v", size, err)
		}
	}
}

func TestTaskOutputDrainsLargeCommands(t *testing.T) {
	output := &taskOutput{}
	size := int64(5 * maxTaskOutputBytes)
	n, err := io.Copy(output, strings.NewReader(strings.Repeat("x", int(size))))
	if err != nil || n != size {
		t.Fatalf("child output was not drained: %d, %v", n, err)
	}
	if output.Len() != maxTaskOutputBytes || !strings.HasSuffix(output.String(), taskOutputTruncated) {
		t.Fatal("unbounded output or missing notice")
	}
	short := &taskOutput{}
	_, _ = short.Write([]byte("short\n"))
	if short.String() != "short\n" {
		t.Fatal("ordinary output changed")
	}
}
