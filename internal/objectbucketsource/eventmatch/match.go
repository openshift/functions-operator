package eventmatch

import "strings"

// MatchEvent checks if an incoming event name (without "s3:" prefix, e.g. "ObjectCreated:Put")
// matches a subscription pattern (with "s3:" prefix, e.g. "s3:ObjectCreated:*").
func MatchEvent(subscriptionPattern string, incomingEventName string) bool {
	pattern := strings.TrimPrefix(subscriptionPattern, "s3:")

	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(incomingEventName, prefix)
	}

	return pattern == incomingEventName
}
