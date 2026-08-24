package service

import "encoding/json"

// itoa formats an int without importing strconv at every call site.
func itoa(n int) string {
	return fmtInt(int64(n))
}

func fmtInt(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// encodeInts serializes a slice of integers deterministically for payload
// hashing and evidence storage.
func encodeInts(v []int64) []byte {
	b, _ := json.Marshal(v)
	return b
}
