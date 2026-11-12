package netsec

import "sync"

// mu 是 sync.Mutex 的别名，便于在守卫结构体中零值直接使用。
type mu = sync.Mutex

// atoi 解析十进制整数，拒绝空串与非数字输入。
func atoi(raw string) (int, error) {
	if raw == "" {
		return 0, errEmptyInt
	}
	value := 0
	negative := false
	digits := 0
	for i, r := range raw {
		if i == 0 && r == '-' {
			negative = true
			continue
		}
		if r < '0' || r > '9' {
			return 0, errInvalidInt
		}
		digits++
		value = value*10 + int(r-'0')
	}
	if digits == 0 {
		return 0, errInvalidInt
	}
	if negative {
		value = -value
	}
	return value, nil
}

var (
	errEmptyInt   = parseError("empty integer")
	errInvalidInt = parseError("invalid integer")
)

type parseError string

func (e parseError) Error() string { return "netsec: " + string(e) }
