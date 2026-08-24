package store

import (
	"strconv"
	"strings"
)

// parseIDSuffix returns the trailing integer of a generated identifier of the
// form "<prefix>-<n>", or 0 when the suffix is absent or not numeric. It is
// shared by the SQLite and in-memory HighWatermarks implementations to reseed
// the service-level sequence generator on restart.
func parseIDSuffix(id string) int64 {
	i := strings.LastIndex(id, "-")
	if i < 0 || i == len(id)-1 {
		return 0
	}
	n, err := strconv.ParseInt(id[i+1:], 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
