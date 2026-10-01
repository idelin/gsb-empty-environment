package splitter

import (
	"context"
	"fmt"
)

// Sink 下游异构存储系统的统一抽象（搜索索引、冷存储、只读库……）。
type Sink interface {
	// Name 存储系统标识，用于路由与日志。
	Name() string
	// Apply 应用一条分流后的事件。
	Apply(ctx context.Context, e Event) error
}

// Router 按事件类型把分流结果路由到不同的异构存储系统。
type Router struct {
	// ArchiveSink 物理归档库（冷存储），接收 Insert 归档包。
	ArchiveSink Sink
	// QuerySink 只读查询库 / 搜索索引，接收 Delete 指令。
	QuerySink Sink
}

// Splitter 逻辑删除分流器：把逻辑删除 Update 事件转换为
// 「归档 Insert + 查询侧 Delete」两路事件并分发。
type Splitter struct {
	Router Router
	// ArchiveTable 归档表名前缀，默认 "archive_"。
	ArchivePrefix string
}

// NewSplitter 构造分流器。
func NewSplitter(archiveSink, querySink Sink) *Splitter {
	return &Splitter{
		Router:        Router{ArchiveSink: archiveSink, QuerySink: querySink},
		ArchivePrefix: "archive_",
	}
}

// Handle 处理一条源库变更事件。
// 逻辑删除时返回分流产生的事件列表；其余事件原样透传。
func (s *Splitter) Handle(ctx context.Context, e Event) ([]Event, error) {
	if !isLogicalDelete(e) {
		return []Event{e}, nil // 非逻辑删除：透传，业务无感知
	}
	if e.Before == nil {
		return nil, fmt.Errorf("splitter: 逻辑删除事件缺少前镜像，无法构造归档包: %v", e)
	}

	// 1) 归档包：完整历史数据（前镜像）+ 删除标记，Insert 进冷存储。
	archiveRow := Row{}
	for k, v := range e.Before {
		archiveRow[k] = v
	}
	archiveRow["is_deleted"] = int64(1)
	archive := Event{
		Type:  EventInsert,
		Table: s.ArchivePrefix + e.Table,
		ID:    e.ID,
		After: archiveRow,
		Raw:   e.Raw,
	}
	// 2) 只读查询库 / 搜索索引：物理删除该 ID。
	del := Event{Type: EventDelete, Table: e.Table, ID: e.ID, Raw: e.Raw}

	if err := s.Router.ArchiveSink.Apply(ctx, archive); err != nil {
		return nil, fmt.Errorf("splitter: 归档到 %s 失败: %v", s.Router.ArchiveSink.Name(), err)
	}
	if err := s.Router.QuerySink.Apply(ctx, del); err != nil {
		return nil, fmt.Errorf("splitter: 删除指令发送到 %s 失败: %v", s.Router.QuerySink.Name(), err)
	}
	return []Event{archive, del}, nil
}
