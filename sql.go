package cdcundo

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// StmtKind 表示补偿语句的种类。
type StmtKind string

const (
	StmtInsert StmtKind = "INSERT"
	StmtUpdate StmtKind = "UPDATE"
	StmtDelete StmtKind = "DELETE"
)

// Statement 是一条可执行的补偿 DML：参数化 SQL + 绑定参数 + 语义载荷。
// SQL 用于落盘成 Undo 脚本；Payload 供执行器直接重放，避免解析 SQL。
type Statement struct {
	Kind    StmtKind      `json:"kind"`
	Table   string        `json:"table"`
	SQL     string        `json:"sql"`
	Args    []interface{} `json:"args"`
	Set     Row           `json:"set,omitempty"`   // INSERT/UPDATE 的赋值列
	Where   Row           `json:"where,omitempty"` // UPDATE/DELETE 的定位条件（主键）
	Comment string        `json:"comment"`
}

// sortedKeys 返回排序后的列名，保证生成的 SQL 确定性（可测试、可 diff）。
func sortedKeys(r Row) []string {
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// quoteIdent 引用标识符（反引号转义）。
func quoteIdent(name string) string {
	return "`" + strings.Replace(name, "`", "``", -1) + "`"
}

// literal 将 Go 值渲染为 SQL 字面量，用于生成可独立执行的 Undo 脚本。
func literal(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case string:
		return "'" + strings.Replace(t, "'", "''", -1) + "'"
	case bool:
		if t {
			return "TRUE"
		}
		return "FALSE"
	case time.Time:
		return "'" + t.UTC().Format("2006-01-02 15:04:05.000") + "'"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// renderInsert 渲染 INSERT 语句。
func renderInsert(table string, set Row) (string, []interface{}) {
	keys := sortedKeys(set)
	cols := make([]string, len(keys))
	holders := make([]string, len(keys))
	args := make([]interface{}, len(keys))
	for i, k := range keys {
		cols[i] = quoteIdent(k)
		holders[i] = "?"
		args[i] = set[k]
	}
	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);",
		quoteIdent(table), strings.Join(cols, ", "), strings.Join(holders, ", "))
	return sql, args
}

// renderUpdate 渲染 UPDATE 语句（SET + WHERE 主键定位）。
func renderUpdate(table string, set, where Row) (string, []interface{}) {
	setKeys := sortedKeys(set)
	setParts := make([]string, len(setKeys))
	args := make([]interface{}, 0, len(set)+len(where))
	for i, k := range setKeys {
		setParts[i] = fmt.Sprintf("%s = ?", quoteIdent(k))
		args = append(args, set[k])
	}
	whereKeys := sortedKeys(where)
	whereParts := make([]string, len(whereKeys))
	for i, k := range whereKeys {
		whereParts[i] = fmt.Sprintf("%s = ?", quoteIdent(k))
		args = append(args, where[k])
	}
	sql := fmt.Sprintf("UPDATE %s SET %s WHERE %s;",
		quoteIdent(table), strings.Join(setParts, ", "), strings.Join(whereParts, " AND "))
	return sql, args
}

// renderDelete 渲染 DELETE 语句（主键定位）。
func renderDelete(table string, where Row) (string, []interface{}) {
	whereKeys := sortedKeys(where)
	whereParts := make([]string, len(whereKeys))
	args := make([]interface{}, len(whereKeys))
	for i, k := range whereKeys {
		whereParts[i] = fmt.Sprintf("%s = ?", quoteIdent(k))
		args[i] = where[k]
	}
	sql := fmt.Sprintf("DELETE FROM %s WHERE %s;",
		quoteIdent(table), strings.Join(whereParts, " AND "))
	return sql, args
}

// inlineSQL 将参数化 SQL 展开为内联字面量形式，便于人工审阅 Undo 脚本。
func inlineSQL(sql string, args []interface{}) string {
	var b strings.Builder
	argIdx := 0
	for i := 0; i < len(sql); i++ {
		if sql[i] == '?' && argIdx < len(args) {
			b.WriteString(literal(args[argIdx]))
			argIdx++
		} else {
			b.WriteByte(sql[i])
		}
	}
	return b.String()
}
