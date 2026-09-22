package server

import "bytes"

const maxTaskOutputBytes = 1 << 20
const taskOutputTruncated = "\n[Komari: output truncated at 1 MiB; redirect large output to a file.]\n"

// Keep draining the child process after reaching the preview limit. Returning
// a short write would kill a command whose only problem is verbose output.
type taskOutput struct {
	data      bytes.Buffer
	truncated bool
}

func (b *taskOutput) Write(data []byte) (int, error) {
	remaining := maxTaskOutputBytes - b.data.Len()
	if len(data) > remaining {
		b.truncated = true
		_, _ = b.data.Write(data[:remaining])
	} else {
		_, _ = b.data.Write(data)
	}
	return len(data), nil
}

func (b *taskOutput) String() string {
	if b.truncated {
		return b.data.String() + taskOutputTruncated
	}
	return b.data.String()
}

func (b *taskOutput) Len() int { return b.data.Len() }
