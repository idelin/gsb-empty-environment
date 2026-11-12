package netsec

import (
	"sync"
	"time"
)

// Syscall 标识归一化后的审计操作类型。
type Syscall string

const (
	// SysMount 对应 mount(2) 审计记录（含 cgroup/cgroup2 文件系统挂载）。
	SysMount Syscall = "mount"
	// SysOpenat 对应 openat(2) 审计记录。
	SysOpenat Syscall = "openat"
	// SysWrite 对应 write(2) 审计记录。
	SysWrite Syscall = "write"
	// SysMigrate 是由 cgroup.procs/cgroup.threads 写入合成出来的迁移记录。
	SysMigrate Syscall = "migrate"
)

// AuditEvent 是主机审计事件流（auditd / eBPF 探针等）归一化后的记录。
//
// 字段保持与具体采集器解耦：采集适配器负责把原始记录翻译成该结构，
// 并尽量填写 ResolvedHostPath，给出宿主机视角的绝对路径，便于在
// cgroup 命名空间内外做一致的路径判定。
type AuditEvent struct {
	// Time 为事件在内核侧产生的时间；零值由检测器补为当前时间。
	Time time.Time
	// Seq 为审计流水中的单调序号，用于稳定排序与事件链展示。
	Seq uint64

	// 进程身份信息。
	PID  uint32
	PPID uint32
	Comm string

	// CgroupNSInode 为进程所在 cgroup namespace 的 inode 号，
	// 0 表示初始（宿主机）cgroup namespace。
	CgroupNSInode uint64
	// HostCgroup 为进程在宿主机视角下所属的 cgroup 路径。
	HostCgroup string

	// Syscall 标识操作类型。
	Syscall Syscall

	// Path 为被访问路径（进程视角）。
	Path string
	// ResolvedHostPath 为可选的宿主机视角绝对路径；
	// 留空时检测器回退到内部挂载表进行解析。
	ResolvedHostPath string

	// MountPoint/MountRoot/FSType/Writable 用于 mount 事件，
	// 也用于维护检测器内部的挂载表。
	MountPoint string
	MountRoot  string
	FSType     string
	// Writable 对 mount 表示非只读挂载，对 openat 表示以写方式打开。
	Writable bool

	// Payload 为 write 的数据预览（如 release_agent 内容、被迁移 PID）。
	Payload string
}

// AuditEventSource 表示可订阅的主机审计事件流。
type AuditEventSource interface {
	// Events 返回事件通道，通道关闭后订阅结束。
	Events() <-chan *AuditEvent
	// Close 关闭事件源。
	Close() error
}

// ChannelSource 是基于内存通道的 AuditEventSource，
// 供采集适配器封装或测试使用。
type ChannelSource struct {
	mu     sync.Mutex
	ch     chan *AuditEvent
	closed bool
}

// NewChannelSource 创建带缓冲的通道事件源。
func NewChannelSource(buffer int) *ChannelSource {
	if buffer < 0 {
		buffer = 0
	}
	return &ChannelSource{
		ch: make(chan *AuditEvent, buffer),
	}
}

// Emit 向事件流投递一条事件；事件源已关闭时返回 false。
func (s *ChannelSource) Emit(e *AuditEvent) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	s.mu.Unlock()
	s.ch <- e
	return true
}

// Events 实现 AuditEventSource。
func (s *ChannelSource) Events() <-chan *AuditEvent { return s.ch }

// Close 实现 AuditEventSource，可重复调用。
func (s *ChannelSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
	return nil
}
