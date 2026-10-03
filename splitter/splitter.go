package splitter

import (
	"context"
	"fmt"
	"log"
)

// Splitter 逻辑删除转物理归档的流式分流中间件。
//
// 它作为业务与下游存储之间的透明中间层：业务只需在源库执行
// `UPDATE user SET is_deleted = 1 WHERE id = ?`，分流器在变更流上
// 拦截该事件并自动完成跨异构存储系统的路由分发，业务无感知。
type Splitter struct {
	router *Router
	logger *log.Logger
}

// New 创建分流器。logger 可为 nil（静默）。
func New(router *Router, logger *log.Logger) *Splitter {
	return &Splitter{router: router, logger: logger}
}

// Process 处理单条事件：
//   - 逻辑删除（is_deleted 置 1 的 Update）→ 归档库 Insert 归档包 + 查询库 Delete
//   - 其他事件 → 透传到下游
func (s *Splitter) Process(ctx context.Context, e Event) error {
	if !e.IsLogicalDelete() {
		return s.router.Dispatch(ctx, RoutePassthrough, e)
	}

	archive := Event{
		Op:    OpInsert,
		Table: e.Table,
		Key:   e.Key,
		After: e.FullImage(), // 归档包：完整历史数据
	}
	if err := s.router.Dispatch(ctx, RouteArchive, archive); err != nil {
		return fmt.Errorf("splitter: 归档失败: %w", err)
	}

	tombstone := Event{Op: OpDelete, Table: e.Table, Key: e.Key, Before: e.After}
	if err := s.router.Dispatch(ctx, RouteQuery, tombstone); err != nil {
		return fmt.Errorf("splitter: 查询库删除失败: %w", err)
	}

	if s.logger != nil {
		s.logger.Printf("splitter: 拦截逻辑删除 %s id=%v → 归档 Insert + 查询库 Delete", e.Table, e.Key)
	}
	return nil
}

// Run 以流式方式消费事件通道，直到通道关闭或 ctx 取消。
// 每条事件的处理错误会发送到 errs（若 errs 为 nil 则中断并返回错误）。
func (s *Splitter) Run(ctx context.Context, in <-chan Event, errs chan<- error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e, ok := <-in:
			if !ok {
				return nil
			}
			if err := s.Process(ctx, e); err != nil {
				if errs == nil {
					return err
				}
				select {
				case errs <- err:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
}
