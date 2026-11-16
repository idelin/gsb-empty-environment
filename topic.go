package netsec

import (
	"strings"
	"unicode/utf8"
)

// TopicMatches reports whether an MQTT topic name matches an MQTT topic
// filter. It implements the '+' and '#' wildcards, including the rule that a
// leading wildcard does not match a topic whose first level starts with '$'.
func TopicMatches(filter, topic string) bool {
	if filter == "" || topic == "" || !utf8.ValidString(filter) || !utf8.ValidString(topic) {
		return false
	}
	if strings.HasPrefix(topic, "$") && (filter[0] == '#' || filter[0] == '+') {
		return false
	}

	filterLevels := strings.Split(filter, "/")
	topicLevels := strings.Split(topic, "/")
	for i, level := range filterLevels {
		if level == "#" {
			return i == len(filterLevels)-1
		}
		if i >= len(topicLevels) {
			return false
		}
		if level != "+" && level != topicLevels[i] {
			return false
		}
	}
	return len(filterLevels) == len(topicLevels)
}

// ValidTopicFilter reports whether filter is a syntactically valid MQTT topic
// filter. It rejects embedded NUL bytes and misplaced wildcards.
func ValidTopicFilter(filter string) bool {
	if filter == "" || !utf8.ValidString(filter) || strings.IndexByte(filter, 0) >= 0 {
		return false
	}
	levels := strings.Split(filter, "/")
	for i, level := range levels {
		if strings.Contains(level, "#") {
			if level != "#" || i != len(levels)-1 {
				return false
			}
		}
		if strings.Contains(level, "+") && level != "+" {
			return false
		}
	}
	return true
}

// ValidTopicName reports whether topic is a syntactically valid MQTT topic
// name (a concrete topic, not a filter).
func ValidTopicName(topic string) bool {
	return topic != "" &&
		utf8.ValidString(topic) &&
		!strings.ContainsAny(topic, "+#") &&
		strings.IndexByte(topic, 0) < 0
}
