package cdcundo

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// quoteIdent 渲染标识符（双引号包裹，内部双引号转义）。
func quoteIdent(s string) string {
	return `"` + strings.Replace(s, `"`, `""`, -1) + `"`
}

// literal 把 Go 值渲染为 SQL 字面量。
func literal(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case string:
		return `'` + strings.Replace(x, `'`, `''`, -1) + `'`
	case bool:
		if x {
			return "TRUE"
		}
		return "FALSE"
	case int:
		return strconv.Itoa(x)
	case int8:
		return strconv.FormatInt(int64(x), 10)
	case int16:
		return strconv.FormatInt(int64(x), 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint:
		return strconv.FormatUint(uint64(x), 10)
	case uint32:
		return strconv.FormatUint(uint64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float32:
		return strconv.FormatFloat(float64(x), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case time.Time:
		// 毫秒级精度的时间字面量。
		return `'` + x.UTC().Format("2006-01-02 15:04:05.000") + `'`
	default:
		return `'` + strings.Replace(fmt.Sprintf("%v", x), `'`, `''`, -1) + `'`
	}
}

// whereKey 渲染主键等值条件。
func whereKey(key Row) string {
	parts := make([]string, 0, len(key))
	for _, col := range key.SortedKeys() {
		parts = append(parts, quoteIdent(col)+" = "+literal(key[col]))
	}
	return strings.Join(parts, " AND ")
}

// renderDelete 生成按主键删除的 SQL。
func renderDelete(table string, key Row) string {
	return fmt.Sprintf("DELETE FROM %s WHERE %s;", quoteIdent(table), whereKey(key))
}

// renderUpdate 生成按主键回写指定列的 SQL。
func renderUpdate(table string, key Row, set Row) string {
	assigns := make([]string, 0, len(set))
	for _, col := range set.SortedKeys() {
		assigns = append(assigns, quoteIdent(col)+" = "+literal(set[col]))
	}
	return fmt.Sprintf("UPDATE %s SET %s WHERE %s;",
		quoteIdent(table), strings.Join(assigns, ", "), whereKey(key))
}

// renderInsert 生成整行插入的 SQL。
func renderInsert(table string, row Row) string {
	cols := row.SortedKeys()
	quoted := make([]string, 0, len(cols))
	vals := make([]string, 0, len(cols))
	for _, c := range cols {
		quoted = append(quoted, quoteIdent(c))
		vals = append(vals, literal(row[c]))
	}
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);",
		quoteIdent(table), strings.Join(quoted, ", "), strings.Join(vals, ", "))
}
