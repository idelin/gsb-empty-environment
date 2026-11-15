package session

import "strings"

// TopicMatch reports whether an MQTT topic filter matches a concrete topic
// name using the MQTT wildcard rules:
//
//   - Levels are separated by '/'.
//   - '+' matches exactly one level.
//   - '#' matches the current level and every level after it and may only
//     appear as the final level (e.g. "sport/#" matches "sport" itself too).
//   - '$' topics (such as "$SYS/...") never match a filter starting with a
//     wildcard.
func TopicMatch(filter, topic string) bool {
	if filter == "" || topic == "" {
		return false
	}
	if topic[0] == '$' && (filter[0] == '+' || filter[0] == '#') {
		return false
	}

	filterParts := strings.Split(filter, "/")
	topicParts := strings.Split(topic, "/")

	for i, fp := range filterParts {
		if fp == "#" {
			return true
		}
		if i >= len(topicParts) {
			return false
		}
		if fp != "+" && fp != topicParts[i] {
			return false
		}
	}
	return len(filterParts) == len(topicParts)
}
