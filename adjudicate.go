package netsec

import (
	"fmt"
	"strconv"
	"strings"
)

// Verdict 是网关对一次请求的裁决结果。
type Verdict struct {
	// Allowed 为 true 表示放行，false 表示拒绝。
	Allowed bool
	// Reason 说明裁决原因，便于网关记录日志。
	Reason string
	// ContentLength 是裁决认定的有效长度；无 content-length 头时为 -1。
	ContentLength int64
}

func allow(effective int64, reason string) Verdict {
	return Verdict{Allowed: true, Reason: reason, ContentLength: effective}
}

func reject(reason string) Verdict {
	return Verdict{Allowed: false, Reason: reason, ContentLength: -1}
}

// Adjudicate 对 content-length 头与数据帧正文做一致性裁决：
//
//   - 存在多个 content-length 且取值不一致时拒绝（请求走私风险）；
//   - 存在 content-length 时，其值必须等于所有数据帧正文长度之和，
//     否则多余字节会被当作下一个请求的头，拒绝；
//   - 单个头且长度相符（或无 content-length 头）时放行。
//
// contentLengthValues 为请求中全部 content-length 头的原始取值
// （重复头按出现顺序逐个传入，逗号合并的形式会被自动拆分）；
// frames 为本次请求收到的数据帧，正文长度为 len(frame)。
func Adjudicate(contentLengthValues []string, frames ...[]byte) Verdict {
	var bodySum int64
	for _, frame := range frames {
		bodySum += int64(len(frame))
	}

	var values []string
	for _, raw := range contentLengthValues {
		for _, part := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				values = append(values, trimmed)
			}
		}
	}

	if len(values) == 0 {
		return allow(-1, "无 content-length 头，放行")
	}

	effective, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || effective < 0 {
		return reject(fmt.Sprintf("非法的 content-length 值 %q", values[0]))
	}

	for _, value := range values[1:] {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 {
			return reject(fmt.Sprintf("非法的 content-length 值 %q", value))
		}
		if parsed != effective {
			return reject(fmt.Sprintf("重复的 content-length 不一致：%d 与 %d", effective, parsed))
		}
	}

	if effective != bodySum {
		return reject(fmt.Sprintf("content-length %d 与数据帧正文总和 %d 不符", effective, bodySum))
	}

	return allow(effective, "content-length 与数据帧正文总和一致，放行")
}
