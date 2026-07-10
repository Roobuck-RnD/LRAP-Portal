package main

import (
	"strings"
)

// getString safely extracts a string value from a map.
// Used by UCI parsing helpers.
func getString(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

// resolveSid chooses the correct ubus SID.
//
// Local AC:
// - Prefer the SID from Authorization header.
// - Fallback to anonymous zero SID.
//
// Remote AP:
// - Use anonymous zero SID because AP ACL is configured for unauthenticated ubus access.
func resolveSid(ip string, headerSid string) string {
	ip = strings.TrimSpace(ip)
	headerSid = strings.TrimSpace(headerSid)

	if ip != "" && ip != "127.0.0.1" && ip != "localhost" {
		return "00000000000000000000000000000000"
	}

	if headerSid != "" {
		return headerSid
	}

	return "00000000000000000000000000000000"
}