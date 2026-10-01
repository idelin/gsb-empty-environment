// Package splitter 实现「逻辑删除转物理归档」流式分流器。
//
// 业务侧照常执行 UPDATE ... SET is_deleted = 1，中间件拦截该事件后，
// 自动分流为：冷存储（物理归档库）的 Insert 归档包 + 只读查询库/搜索索引的
// Delete 指令，业务无感知。
package splitter

import "fmt"

// EventType 变更事件类型。
type EventType string

const (
	EventInsert EventType = "INSERT"
	EventUpdate EventType = "UPDATE"
	EventDelete EventType = "DELETE"
)

// Row 一行数据的列值映射。
type Row map[string]interface{}

// Event 源库产生的一条变更事件（binlog 风格，携带前后镜像）。
type Event struct {
	Type   EventType // 事件类型
	Table  string    // 表名
	ID     int64     // 主键
	Before Row       // 变更前镜像（Update 时携带）
	After  Row       // 变更后镜像
	Raw    string    // 原始 SQL（便于审计追溯）
}

func (e Event) String() string {
	return fmt.Sprintf("%s %s id=%d", e.Type, e.Table, e.ID)
}

// isLogicalDelete 判定是否为「逻辑删除」：is_deleted 被置为 1。
// 要求前置镜像中 is_deleted 不为 1（无前置镜像时按新删除处理），
// 避免对重复置位产生重复归档。
func isLogicalDelete(e Event) bool {
	if e.Type != EventUpdate || e.After == nil {
		return false
	}
	if toInt64(e.After["is_deleted"]) != 1 {
		return false
	}
	if e.Before == nil {
		return true
	}
	return toInt64(e.Before["is_deleted"]) != 1
}

func toInt64(v interface{}) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	case string:
		if n == "1" {
			return 1
		}
	}
	return 0
}
