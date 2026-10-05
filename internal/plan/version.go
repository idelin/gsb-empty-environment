package plan

import (
	"fmt"
	"strconv"
	"strings"
)

// Version 是一个规范化的三段语义版本号。
type Version struct {
	Parts [3]int
}

// ParseVersion 解析 "1"、"1.2"、"1.2.3" 形式的版本号，缺省段补零。
func ParseVersion(s string) (Version, error) {
	var v Version
	s = strings.TrimSpace(s)
	if s == "" {
		return v, fmt.Errorf("空版本号")
	}
	fields := strings.Split(s, ".")
	if len(fields) > 3 {
		return v, fmt.Errorf("版本号 %q 超过三段", s)
	}
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return v, fmt.Errorf("版本号 %q 含非法段 %q", s, f)
		}
		v.Parts[i] = n
	}
	return v, nil
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Parts[0], v.Parts[1], v.Parts[2])
}

// Compare 返回 -1/0/1。
func (v Version) Compare(o Version) int {
	for i := 0; i < 3; i++ {
		if v.Parts[i] < o.Parts[i] {
			return -1
		}
		if v.Parts[i] > o.Parts[i] {
			return 1
		}
	}
	return 0
}
