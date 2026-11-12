package netsec

import (
	"errors"
	"strconv"
	"strings"
)

// FIX 标准字段 Tag。
const (
	TagMsgSeqNum       = 34
	TagMsgType         = 35
	TagPossDupFlag     = 43
	TagSendingTime     = 52
	TagOrigSendingTime = 122
	TagResetSeqNumFlag = 141
)

// SOH 是 FIX 报文的字段分隔符。
const SOH = '\x01'

var (
	errEmptyMessage = errors.New("netsec: empty fix message")
	errMalformedTag = errors.New("netsec: malformed fix field")
	errInvalidTag   = errors.New("netsec: non-numeric fix tag")
)

// Field 表示一个按报文原始顺序保留的 FIX 键值对。
type Field struct {
	Tag   int
	Value string
}

// Message 是只做字段切分的极简 FIX 报文视图，
// 不校验 BodyLength(9) 与 CheckSum(10)，供会话层只读判断使用。
type Message struct {
	fields []Field
	index  map[int]int
}

// ParseMessage 按 SOH 切分原始 FIX 报文。
func ParseMessage(raw []byte) (*Message, error) {
	if len(raw) == 0 {
		return nil, errEmptyMessage
	}

	parts := strings.Split(string(raw), string(SOH))
	fields := make([]Field, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue // 容忍结尾 SOH 产生的空段
		}
		eq := strings.IndexByte(part, '=')
		if eq <= 0 {
			return nil, errMalformedTag
		}
		tag, err := strconv.Atoi(part[:eq])
		if err != nil {
			return nil, errInvalidTag
		}
		fields = append(fields, Field{Tag: tag, Value: part[eq+1:]})
	}
	if len(fields) == 0 {
		return nil, errEmptyMessage
	}

	index := make(map[int]int, len(fields))
	for i, field := range fields {
		if _, ok := index[field.Tag]; !ok {
			index[field.Tag] = i
		}
	}
	return &Message{fields: fields, index: index}, nil
}

// Get 返回指定 Tag 的首个值；不存在时 ok 为 false。
func (m *Message) Get(tag int) (value string, ok bool) {
	i, ok := m.index[tag]
	if !ok {
		return "", false
	}
	return m.fields[i].Value, true
}

// Has 判断指定 Tag 是否出现。
func (m *Message) Has(tag int) bool {
	_, ok := m.index[tag]
	return ok
}
