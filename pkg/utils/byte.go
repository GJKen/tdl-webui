package utils

import (
	"fmt"
	"strconv"
	"strings"
)

type _byte struct{}

var Byte _byte

func (b _byte) FormatBinaryBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.2f KB", float64(n)/1024)
	}
	if n < 1024*1024*1024 {
		return fmt.Sprintf("%.2f MB", float64(n)/1024/1024)
	}
	if n < 1024*1024*1024*1024 {
		return fmt.Sprintf("%.2f GB", float64(n)/1024/1024/1024)
	}
	return fmt.Sprintf("%.2f TB", float64(n)/1024/1024/1024/1024)
}

// Parse parses a human-friendly byte size string into a number of bytes.
// It accepts a bare number ("1048576"), or a number with a binary unit suffix
// (case-insensitive, optional space): B, K/KB/KIB, M/MB/MIB, G/GB/GIB,
// T/TB/TIB — all 1024-based to match FormatBinaryBytes. Examples: "10M",
// "500K", "2.5MB", "1.5 GiB". An empty string parses to 0 (meaning "no value").
func (b _byte) Parse(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	// split into the leading numeric part and the trailing unit
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	numStr, unit := s[:i], strings.TrimSpace(s[i:])
	if numStr == "" {
		return 0, fmt.Errorf("invalid byte size %q: missing number", s)
	}

	num, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid byte size %q: %w", s, err)
	}
	if num < 0 {
		return 0, fmt.Errorf("invalid byte size %q: must not be negative", s)
	}

	var mult float64
	switch strings.ToUpper(unit) {
	case "", "B":
		mult = 1
	case "K", "KB", "KIB":
		mult = 1024
	case "M", "MB", "MIB":
		mult = 1024 * 1024
	case "G", "GB", "GIB":
		mult = 1024 * 1024 * 1024
	case "T", "TB", "TIB":
		mult = 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("invalid byte size %q: unknown unit %q", s, unit)
	}

	return int64(num * mult), nil
}
