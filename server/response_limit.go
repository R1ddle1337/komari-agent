package server

import (
	"errors"
	"io"
)

// Panel control responses contain metadata and queued commands, not file data.
// Bound decompressed bodies as well, since net/http may transparently gunzip.
const maxControlResponseBytes = 16 << 20

var errControlResponseTooLarge = errors.New("panel control response exceeds 16 MiB")

func readControlResponse(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxControlResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxControlResponseBytes {
		return nil, errControlResponseTooLarge
	}
	return data, nil
}
