package netsec

import (
	"errors"
	"strconv"
)

// BEP 9 消息只使用整数、字节串、列表和字典这四种 bencode 类型。
type (
	bInt    int64
	bString []byte
	bList   []any
	bDict   map[string]any
)

var (
	errBencodeTruncated = errors.New("netsec: truncated bencode data")
	errBencodeInvalid   = errors.New("netsec: invalid bencode encoding")
	errBencodeTooDeep   = errors.New("netsec: bencode nesting too deep")
	errBencodeTrailing  = errors.New("netsec: trailing bytes after bencode value")
)

// maxBencodeDepth 限制递归深度，防止对端用深层嵌套结构耗尽栈空间。
const maxBencodeDepth = 16

func decodeBencode(data []byte) (any, error) {
	value, rest, err := decodeBencodeValue(data, 0)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, errBencodeTrailing
	}
	return value, nil
}

// decodeBencodeValue 返回解析出的值以及该值之后剩余的字节，
// 以便 ut_metadata data 消息取出 bencode 头之后的分片负载。
func decodeBencodeValue(data []byte, depth int) (any, []byte, error) {
	if depth > maxBencodeDepth {
		return nil, nil, errBencodeTooDeep
	}
	if len(data) == 0 {
		return nil, nil, errBencodeTruncated
	}
	switch data[0] {
	case 'i':
		return decodeBencodeInt(data)
	case 'l':
		return decodeBencodeList(data, depth)
	case 'd':
		return decodeBencodeDict(data, depth)
	default:
		if data[0] >= '0' && data[0] <= '9' {
			return decodeBencodeString(data)
		}
		return nil, nil, errBencodeInvalid
	}
}

func decodeBencodeInt(data []byte) (bInt, []byte, error) {
	end := indexByte(data, 'e')
	if end < 0 {
		return 0, nil, errBencodeTruncated
	}
	body := string(data[1:end])
	if body == "" || body == "-" {
		return 0, nil, errBencodeInvalid
	}
	if len(body) > 1 && (body[0] == '0' || (body[0] == '-' && body[1] == '0')) {
		return 0, nil, errBencodeInvalid
	}
	for i := 0; i < len(body); i++ {
		if body[i] == '-' {
			if i != 0 {
				return 0, nil, errBencodeInvalid
			}
			continue
		}
		if body[i] < '0' || body[i] > '9' {
			return 0, nil, errBencodeInvalid
		}
	}
	value, err := strconv.ParseInt(body, 10, 64)
	if err != nil {
		return 0, nil, errBencodeInvalid
	}
	return bInt(value), data[end+1:], nil
}

func decodeBencodeString(data []byte) (bString, []byte, error) {
	colon := indexByte(data, ':')
	if colon < 0 {
		return nil, nil, errBencodeTruncated
	}
	lengthText := string(data[:colon])
	if lengthText == "" || (len(lengthText) > 1 && lengthText[0] == '0') {
		return nil, nil, errBencodeInvalid
	}
	for _, ch := range lengthText {
		if ch < '0' || ch > '9' {
			return nil, nil, errBencodeInvalid
		}
	}
	length, err := strconv.ParseUint(lengthText, 10, 64)
	if err != nil {
		return nil, nil, errBencodeInvalid
	}
	start := colon + 1
	if uint64(len(data)-start) < length {
		return nil, nil, errBencodeTruncated
	}
	value := make(bString, length)
	copy(value, data[start:start+int(length)])
	return value, data[start+int(length):], nil
}

func decodeBencodeList(data []byte, depth int) (bList, []byte, error) {
	rest := data[1:]
	values := bList{}
	for {
		if len(rest) == 0 {
			return nil, nil, errBencodeTruncated
		}
		if rest[0] == 'e' {
			return values, rest[1:], nil
		}
		value, next, err := decodeBencodeValue(rest, depth+1)
		if err != nil {
			return nil, nil, err
		}
		values = append(values, value)
		rest = next
	}
}

func decodeBencodeDict(data []byte, depth int) (bDict, []byte, error) {
	rest := data[1:]
	dict := bDict{}
	var previousKey string
	first := true
	for {
		if len(rest) == 0 {
			return nil, nil, errBencodeTruncated
		}
		if rest[0] == 'e' {
			return dict, rest[1:], nil
		}
		keyValue, next, err := decodeBencodeString(rest)
		if err != nil {
			return nil, nil, err
		}
		key := string(keyValue)
		if !first && key <= previousKey {
			return nil, nil, errBencodeInvalid
		}
		value, next, err := decodeBencodeValue(next, depth+1)
		if err != nil {
			return nil, nil, err
		}
		dict[key] = value
		previousKey = key
		first = false
		rest = next
	}
}

func indexByte(data []byte, target byte) int {
	for i, b := range data {
		if b == target {
			return i
		}
	}
	return -1
}

func dictLookup(dict bDict, key string) (any, bool) {
	value, ok := dict[key]
	return value, ok
}

func dictInt(dict bDict, key string) (int64, bool, error) {
	value, ok := dictLookup(dict, key)
	if !ok {
		return 0, false, nil
	}
	number, ok := value.(bInt)
	if !ok {
		return 0, true, errBencodeInvalid
	}
	return int64(number), true, nil
}
