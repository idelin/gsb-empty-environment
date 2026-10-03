// Package splitter 实现"逻辑删除转物理归档"流式分流器。
//
// 作为业务逻辑标记处理中间件，它拦截表中将 is_deleted 置为 1 的
// Update 事件，并自动转换为：
//   - 下游物理归档库（冷存储）的 Insert 事件（携带完整历史数据的归档包）
//   - 只读查询库 / 搜索索引的 Delete 事件
//
// 分流器以流式方式工作，支持跨异构存储系统的路由分发，对业务无感知。
package splitter

// Op 表示变更事件的操作类型。
type Op string

const (
	OpInsert Op = "INSERT"
	OpUpdate Op = "UPDATE"
	OpDelete Op = "DELETE"
)

// Event 表示源库变更数据流（CDC）中的一条事件。
type Event struct {
	Op     Op             // 操作类型
	Table  string         // 表名
	Key    any            // 主键
	Before map[string]any // 变更前行镜像（Update/Delete 时存在）
	After  map[string]any // 变更后行镜像（Insert/Update 时存在）
}

// IsLogicalDelete 判断事件是否为"逻辑删除"：
// 即 Update 事件将 is_deleted 从非 1 置为 1。
func (e Event) IsLogicalDelete() bool {
	if e.Op != OpUpdate {
		return false
	}
	return asInt(e.After["is_deleted"]) == 1 && asInt(e.Before["is_deleted"]) != 1
}

// FullImage 返回该行的完整历史数据镜像（Before 与 After 合并，After 优先），
// 用于构造归档包。
func (e Event) FullImage() map[string]any {
	full := make(map[string]any, len(e.Before)+len(e.After))
	for k, v := range e.Before {
		full[k] = v
	}
	for k, v := range e.After {
		full[k] = v
	}
	return full
}

func asInt(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	case bool:
		if n {
			return 1
		}
		return 0
	default:
		return 0
	}
}
