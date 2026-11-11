// Package ring 提供按帧号索引的固定容量环形缓冲，
// 用于回滚窗口内的输入与状态快照存储。
package ring

// Ring 保存一个随帧号单调推进的固定容量窗口。
//
// 帧号只增不减：Put 一个更新的帧时，窗口原点按需前移、淘汰旧帧；
// 重写窗口内已存在的帧（回滚恢复的常见操作）不会移动原点，因此不会
// 误淘汰同一窗口内的其他快照。零值不可用，必须用 New 构造。
type Ring[T any] struct {
	data     []T
	origin   int // 槽位 0 对应的帧号
	capacity int
}

// New 创建环形缓冲。origin 为首个可访问帧号（更早的帧视为已淘汰）。
func New[T any](capacity, origin int) *Ring[T] {
	return &Ring[T]{
		data:     make([]T, capacity),
		origin:   origin,
		capacity: capacity,
	}
}

func (r *Ring[T]) index(frame int) int {
	// 物理槽位由帧号直接取模决定，与 origin 无关：
	// 同一帧号永远落到同一槽位，origin 滑动不会让窗口内帧错位。
	return frame % r.capacity
}

// Put 写入某一帧的值。
func (r *Ring[T]) Put(frame int, value T) {
	if frame >= r.origin+r.capacity {
		// 窗口前移：让 frame 落在最后一个槽位，更早的帧全部淘汰。
		// 仅当写入的是“新帧”时才滑动，重写窗口内旧帧保持原点不变。
		r.origin = frame - r.capacity + 1
	}
	r.data[r.index(frame)] = value
}

// Get 读取某一帧的值。
func (r *Ring[T]) Get(frame int) T {
	return r.data[r.index(frame)]
}

// Oldest 返回仍未被覆盖的最早帧号。
func (r *Ring[T]) Oldest() int {
	return r.origin
}

// Contains 判断该帧是否仍在缓冲窗口内（未被覆盖）。
// 缓冲只保存 [Oldest, Oldest+capacity) 范围的帧槽。
func (r *Ring[T]) Contains(frame int) bool {
	return frame >= r.origin && frame < r.origin+r.capacity
}
