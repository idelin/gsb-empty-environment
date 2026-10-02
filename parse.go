package causal

import (
	"fmt"
	"strings"
	"time"
)

// ParseLogLine 解析一行追踪日志，格式：
//   <RFC3339时间戳> <服务器> <SEND|RECV> <消息ID> <对端服务器>
// 例如：
//   2026-10-02T12:00:00+08:00 serverA SEND msg-1 serverB
// 时间戳可带任意时区，解析时统一归一到绝对时刻。
func ParseLogLine(line string) (Event, error) {
	var ev Event
	fields := strings.Fields(line)
	if len(fields) != 5 {
		return ev, fmt.Errorf("causal: bad log line %q: want 5 fields, got %d", line, len(fields))
	}
	t, err := time.Parse(time.RFC3339, fields[0])
	if err != nil {
		return ev, fmt.Errorf("causal: bad timestamp in %q: %v", line, err)
	}
	var kind Kind
	switch strings.ToUpper(fields[2]) {
	case "SEND":
		kind = Send
	case "RECV":
		kind = Recv
	default:
		return ev, fmt.Errorf("causal: bad kind %q in %q", fields[2], line)
	}
	return Event{
		Server: fields[1],
		Kind:   kind,
		Time:   t,
		MsgID:  fields[3],
		Peer:   fields[4],
	}, nil
}

// ParseLog 解析多行日志，空行与 # 开头的注释行被忽略。
func ParseLog(text string) ([]Event, error) {
	var events []Event
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ev, err := ParseLogLine(line)
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, nil
}
