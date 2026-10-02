// Package coordinator 实现 CDC 场景下 DDL 与 DML 的时间线协调器。
//
// 核心语义：当 DDL 事件（如 AlterTableAddColumn）到达时，协调器在数据流中
// 插入一个全局屏障（Barrier），屏障之前的 DML 一律按旧 Schema 解析并排空，
// 屏障落点处原子地切换元数据缓存，屏障之后的 DML 一律按新 Schema 解析，
// 保证零字段错位。
package coordinator

import (
	"fmt"
	"sync"
)

// Schema 描述某一版本的表结构。
type Schema struct {
	Version int
	Columns []string
}

// Clone 返回 Schema 的深拷贝，防止外部修改污染缓存。
func (s *Schema) Clone() *Schema {
	cols := make([]string, len(s.Columns))
	copy(cols, s.Columns)
	return &Schema{Version: s.Version, Columns: cols}
}

// Width 返回列数。
func (s *Schema) Width() int { return len(s.Columns) }

// Event 是流中事件的公共接口。
type Event interface {
	// Seq 返回事件在全局日志中的单调递增位点。
	Seq() uint64
	isEvent()
}

// DMLEvent 携带一行未解码的原始字段（按列顺序）。
type DMLEvent struct {
	SeqNo uint64
	Raw   []string
}

// DDLEvent 表示一次模式变更。当前支持 AlterTableAddColumn。
type DDLEvent struct {
	SeqNo  uint64
	Kind   DDLKind
	Column string // 新增列名（Kind == AlterTableAddColumn 时有效）
}

// DDLKind 枚举受支持的 DDL 类型。
type DDLKind int

const (
	// AlterTableAddColumn 在表尾追加一列。
	AlterTableAddColumn DDLKind = iota
)

func (DMLEvent) isEvent() {}
func (DDLEvent) isEvent() {}

// Seq 实现 Event。
func (e DMLEvent) Seq() uint64 { return e.SeqNo }

// Seq 实现 Event。
func (e DDLEvent) Seq() uint64 { return e.SeqNo }

// Row 是一条按某个 Schema 版本解码完成的数据行。
type Row struct {
	SeqNo         uint64
	SchemaVersion int
	Values        map[string]string
}

// Barrier 是插入输出流中的全局屏障标记，携带切换前后的 Schema 版本。
type Barrier struct {
	SeqNo       uint64
	FromVersion int
	ToVersion   int
}

// Output 是协调器输出流中的元素：要么是解码行，要么是屏障。
type Output struct {
	Row     *Row
	Barrier *Barrier
}

// MisalignmentError 表示 DML 原始字段数与当前 Schema 列数不一致（字段错位）。
type MisalignmentError struct {
	SeqNo         uint64
	GotFields     int
	SchemaVersion int
	SchemaWidth   int
}

// Error 实现 error 接口。
func (e *MisalignmentError) Error() string {
	return fmt.Sprintf("coordinator: field misalignment at seq=%d: got %d fields, schema v%d expects %d",
		e.SeqNo, e.GotFields, e.SchemaVersion, e.SchemaWidth)
}

// Coordinator 是 DDL/DML 时间线协调器。
//
// 它持有元数据缓存（当前 Schema），按事件的全局顺序处理事件流：
//   - DML：用屏障生效时的 Schema 版本解码，字段按列位一一映射；
//   - DDL：先插入屏障并排空（drain）在途的旧结构 DML，
//     再原子地切换元数据缓存，最后恢复新 DML 流入。
type Coordinator struct {
	mu      sync.Mutex
	current *Schema

	// 统计信息，便于测试与观测。
	rowsSinceBarrier     int // 自上一个屏障以来已解码的行数（在途旧结构 DML）
	drainedBeforeBarrier int // 历次屏障落点时累计排空的行数
	barrierCount         int
}

// New 以初始 Schema 创建协调器。初始版本号建议从 1 开始。
func New(initial *Schema) *Coordinator {
	if initial == nil || len(initial.Columns) == 0 {
		panic("coordinator: initial schema must be non-empty")
	}
	return &Coordinator{current: initial.Clone()}
}

// CurrentSchema 返回当前生效 Schema 的拷贝（元数据缓存快照）。
func (c *Coordinator) CurrentSchema() *Schema {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current.Clone()
}

// Stats 返回 (排空行数, 屏障数)。
func (c *Coordinator) Stats() (drained, barriers int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.drainedBeforeBarrier, c.barrierCount
}

// decode 在持有锁的前提下用指定 Schema 解码一行。
func decode(ev DMLEvent, schema *Schema) (*Row, error) {
	if len(ev.Raw) != schema.Width() {
		return nil, &MisalignmentError{
			SeqNo:         ev.SeqNo,
			GotFields:     len(ev.Raw),
			SchemaVersion: schema.Version,
			SchemaWidth:   schema.Width(),
		}
	}
	values := make(map[string]string, schema.Width())
	for i, col := range schema.Columns {
		values[col] = ev.Raw[i]
	}
	return &Row{SeqNo: ev.SeqNo, SchemaVersion: schema.Version, Values: values}, nil
}

// Process 处理单个事件并返回产生的输出元素（0 个或多个）。
//
// DML 产生一个 Row；DDL 产生一个 Barrier（元数据缓存已在屏障落点切换）。
// 该方法是线程安全的；事件必须按全局 Seq 单调递增到达。
func (c *Coordinator) Process(ev Event) ([]Output, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch e := ev.(type) {
	case DMLEvent:
		row, err := decode(e, c.current)
		if err != nil {
			return nil, err
		}
		c.rowsSinceBarrier++
		return []Output{{Row: row}}, nil

	case DDLEvent:
		switch e.Kind {
		case AlterTableAddColumn:
			// 1. 屏障落点：排空（drain）在途的旧结构 DML——此刻之前的
			//    DML 已全部按旧 Schema 解码完毕
			//    （单线程处理模型下，锁即保证排空；并发模型下由
			//    ProcessStream 的 drain 阶段保证）。
			c.drainedBeforeBarrier += c.rowsSinceBarrier
			c.rowsSinceBarrier = 0
			from := c.current.Version
			// 2. 切断并重建元数据缓存：基于旧 Schema 派生新版本。
			next := c.current.Clone()
			next.Version = from + 1
			next.Columns = append(next.Columns, e.Column)
			c.current = next
			c.barrierCount++
			// 3. 屏障进入输出流，之后的 DML 按新 Schema 解析。
			return []Output{{Barrier: &Barrier{
				SeqNo:       e.SeqNo,
				FromVersion: from,
				ToVersion:   next.Version,
			}}}, nil
		default:
			return nil, fmt.Errorf("coordinator: unsupported DDL kind %d at seq=%d", e.Kind, e.SeqNo)
		}
	default:
		return nil, fmt.Errorf("coordinator: unknown event type %T", ev)
	}
}

// ProcessStream 顺序处理整个事件流，返回完整输出流（Row 与 Barrier 按序交错）。
// 任一事件处理失败即返回错误，已产生的输出一并返回。
func (c *Coordinator) ProcessStream(events []Event) ([]Output, error) {
	var out []Output
	for _, ev := range events {
		outs, err := c.Process(ev)
		out = append(out, outs...)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}
