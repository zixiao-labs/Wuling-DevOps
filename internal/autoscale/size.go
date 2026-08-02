package autoscale

import (
	"strconv"
	"strings"
)

// ParseSizeGiB parses a tier/pool size string into whole GiB, rounding up.
// Accepts "80Gi"/"80GiB"/"80G"/"80GB"/"81920Mi" and a bare number (GiB).
// Binary units (Ki/Mi/Gi/Ti) are 1024-based, decimal ones (K/M/G/T, KB/MB/…)
// are 1000-based, matching Kubernetes quantity conventions. ok=false on an
// empty or unparseable string — callers then omit the parameter and let the
// provider apply its own default rather than send a bogus disk size.
func ParseSizeGiB(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n <= 0 {
			return 0, false
		}
		return int(n), true
	}
	bytes, ok := ParseSizeBytes(s)
	if !ok {
		return 0, false
	}
	gib := (bytes + (1 << 30) - 1) >> 30
	if gib == 0 {
		return 0, false
	}
	return int(gib), true
}

// ParseSizeBytes parses the same forms into bytes; a bare number is bytes.
func ParseSizeBytes(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}

	// Bare integer: bytes.
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, true
	}

	i := 0
	for i < len(s) && (s[i] == '.' || s[i] >= '0' && s[i] <= '9') {
		i++
	}
	if i == 0 {
		return 0, false
	}
	numStr := strings.TrimSpace(s[:i])
	suffix := strings.ToLower(strings.TrimSpace(s[i:]))
	if numStr == "" {
		return 0, false
	}

	num, err := strconv.ParseFloat(numStr, 64)
	if err != nil || num <= 0 {
		return 0, false
	}

	var mult float64
	switch suffix {
	case "", "b":
		mult = 1
	case "k", "kb":
		mult = 1000
	case "ki", "kib":
		mult = 1024
	case "m", "mb":
		mult = 1000 * 1000
	case "mi", "mib":
		mult = 1024 * 1024
	case "g", "gb":
		mult = 1000 * 1000 * 1000
	case "gi", "gib":
		mult = 1024 * 1024 * 1024
	case "t", "tb":
		mult = 1000 * 1000 * 1000 * 1000
	case "ti", "tib":
		mult = 1024 * 1024 * 1024 * 1024
	default:
		return 0, false
	}

	return int64(num * mult), true
}
