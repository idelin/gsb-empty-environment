package splitter

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// 匹配 UPDATE <table> SET <assignments> WHERE id = <n>（大小写不敏感）。
var updateRe = regexp.MustCompile(`(?is)^\s*UPDATE\s+` + "`?" + `(\w+)` + "`?" + `\s+SET\s+(.+?)\s+WHERE\s+` + "`?" + `id` + "`?" + `\s*=\s*(\d+)\s*;?\s*$`)

// ParseUpdate 将一条 UPDATE ... WHERE id = N 语句解析为 Update 事件的
// 「后镜像」部分（SET 子句），主键取自 WHERE id = N。
// 仅支持按主键更新的简单语句，足以覆盖逻辑删除场景。
func ParseUpdate(sql string) (table string, id int64, set Row, err error) {
	m := updateRe.FindStringSubmatch(sql)
	if m == nil {
		return "", 0, nil, fmt.Errorf("splitter: 无法解析的 UPDATE 语句: %q", sql)
	}
	table = m[1]
	id, err = strconv.ParseInt(m[3], 10, 64)
	if err != nil {
		return "", 0, nil, fmt.Errorf("splitter: 非法主键: %v", err)
	}
	set = Row{}
	for _, assign := range strings.Split(m[2], ",") {
		kv := strings.SplitN(assign, "=", 2)
		if len(kv) != 2 {
			return "", 0, nil, fmt.Errorf("splitter: 非法赋值子句: %q", assign)
		}
		col := strings.Trim(strings.TrimSpace(kv[0]), "`")
		set[col] = parseLiteral(strings.TrimSpace(kv[1]))
	}
	return table, id, set, nil
}

// parseLiteral 解析 SET 子句中的字面量：整数、NULL、单引号字符串。
func parseLiteral(s string) interface{} {
	if strings.EqualFold(s, "NULL") {
		return nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.Replace(s[1:len(s)-1], `''`, `'`, -1)
	}
	return s
}
