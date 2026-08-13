package util

import "time"

// NowRFC3339 returns the current time in RFC3339 format.
func NowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// ParseRFC3339 parses an RFC3339 time string.
func ParseRFC3339(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}
