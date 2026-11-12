package netsec

import (
	"context"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// EscapeRule 标识命中的 cgroup 逃逸手法。
type EscapeRule string

const (
	// RuleReleaseAgentWrite 指向 cgroup v1 release_agent 文件写入，
	// 通常配合 notify_on_release 实现宿主侧命令执行。
	RuleReleaseAgentWrite EscapeRule = "release_agent_write"
	// RuleNotifyOnRelease 指向以写方式打开 notify_on_release。
	RuleNotifyOnRelease EscapeRule = "notify_on_release_open"
	// RuleWritableCgroupMount 指向沙箱内挂载可写的 cgroup/cgroup2 文件系统。
	RuleWritableCgroupMount EscapeRule = "writable_cgroup_mount"
	// RuleMigrateToParent 指向把进程迁入沙箱 cgroup 根之外（含上级）。
	RuleMigrateToParent EscapeRule = "migrate_to_parent"
)

// 关联窗口与每个沙箱保留的最大历史事件数。
const (
	defaultChainWindow = 30 * time.Second
	defaultChainLimit  = 64
)

// SandboxConfig 描述一个被监控沙箱任务。
type SandboxConfig struct {
	// ID 为沙箱任务标识。
	ID string
	// CgroupNSInode 为沙箱 cgroup namespace 的 inode 号；
	// 与审计事件中的同名字段匹配。为 0 时仅按 HostCgroupRoot 匹配。
	CgroupNSInode uint64
	// HostCgroupRoot 为沙箱在宿主机视角下的 cgroup 根路径，
	// 例如 /sys/fs/cgroup/kubepods/pod-xxx/sandbox。
	HostCgroupRoot string
}

// Alert 是一次逃逸命中的完整描述，会传给调用方的阻断回调。
type Alert struct {
	// Rule 为命中的规则。
	Rule EscapeRule
	// SandboxID 为归属沙箱；无法归属时为空。
	SandboxID string
	// Reason 为可读的命中说明。
	Reason string
	// Time 为命中时间。
	Time time.Time
	// Trigger 为触发告警的审计事件。
	Trigger *AuditEvent
	// Chain 为按 Seq 排序的关联事件链，包含 Trigger。
	Chain []*AuditEvent
}

// BlockFunc 是调用方注册的阻断回调。
// 回调在检测器处理事件的 goroutine 中同步执行，应快速返回、避免阻塞。
type BlockFunc func(Alert)

// Config 为检测器配置。
type Config struct {
	// Sandboxes 为受监控沙箱列表。
	Sandboxes []SandboxConfig
	// Block 为命中后的阻断回调。
	Block BlockFunc
	// ChainWindow 为事件链关联窗口；零值使用默认 30s。
	ChainWindow time.Duration
	// ChainLimit 为事件链最大长度；零值使用默认 64。
	ChainLimit int
	// Now 为可选的时钟函数，主要用于测试。
	Now func() time.Time
}

type mountInfo struct {
	fsType   string
	root     string
	writable bool
}

type managerKey struct {
	nsInode uint64
	comm    string
}

// Detector 订阅主机审计事件流并检测 cgroup 逃逸行为。
//
// 检测器通过 cgroup namespace inode 与宿主 cgroup 路径双重信息，
// 区分事件发生在沙箱 cgroup 命名空间内还是宿主机命名空间；
// 宿主机上注册过的合法 cgroup 管理器（如 systemd、kubelet、containerd）
// 执行的 v2 子树管理动作会被放行。
type Detector struct {
	cfg Config

	mu sync.Mutex
	// sandboxes 按 namespace inode 与宿主 cgroup 根建索引。
	byNS   map[uint64]*SandboxConfig
	byRoot map[string]*SandboxConfig
	// managers 记录宿主机上合法的 cgroup 管理器身份。
	managers map[managerKey]struct{}
	// mounts 为挂载点 -> 挂载信息，用于解析进程视角路径。
	mounts map[string]mountInfo
	// history 为每个沙箱近期事件，用于还原事件链。
	history map[string][]*AuditEvent
}

// NewDetector 创建检测器。
func NewDetector(cfg Config) *Detector {
	if cfg.ChainWindow <= 0 {
		cfg.ChainWindow = defaultChainWindow
	}
	if cfg.ChainLimit <= 0 {
		cfg.ChainLimit = defaultChainLimit
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	d := &Detector{
		cfg:      cfg,
		byNS:     make(map[uint64]*SandboxConfig),
		byRoot:   make(map[string]*SandboxConfig),
		managers: make(map[managerKey]struct{}),
		mounts:   make(map[string]mountInfo),
		history:  make(map[string][]*AuditEvent),
	}
	for i := range cfg.Sandboxes {
		sb := cfg.Sandboxes[i]
		if sb.CgroupNSInode != 0 {
			d.byNS[sb.CgroupNSInode] = &sb
		}
		if sb.HostCgroupRoot != "" {
			d.byRoot[cleanPath(sb.HostCgroupRoot)] = &sb
		}
	}
	return d
}

// AllowManager 登记合法的宿主机 cgroup 管理器（如 systemd、kubelet）。
// 这些进程在初始 cgroup namespace 内执行的挂载与 v2 子树管理一律放行。
func (d *Detector) AllowManager(nsInode uint64, comm string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.managers[managerKey{nsInode: nsInode, comm: comm}] = struct{}{}
}

// Run 消费事件源直到通道关闭或 ctx 取消。
func (d *Detector) Run(ctx context.Context, source AuditEventSource) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-source.Events():
			if !ok {
				return nil
			}
			d.ProcessEvent(ev)
		}
	}
}

// ProcessEvent 处理单条审计事件，命中规则时同步调用阻断回调。
// 该方法可并发调用。
func (d *Detector) ProcessEvent(ev *AuditEvent) {
	if ev == nil {
		return
	}
	if ev.Time.IsZero() {
		ev.Time = d.cfg.Now()
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// 挂载事件始终先维护挂载表，保证后续路径解析可用。
	if ev.Syscall == SysMount && ev.MountPoint != "" && isCgroupFSType(ev.FSType) {
		d.mounts[cleanPath(ev.MountPoint)] = mountInfo{
			fsType:   ev.FSType,
			root:     cleanPath(ev.MountRoot),
			writable: ev.Writable,
		}
	}

	// 宿主机命名空间内、登记过的合法 cgroup 管理器：
	// 其挂载与 cgroup v2 子树管理动作属于正常运维，直接放行。
	if _, ok := d.managers[managerKey{nsInode: ev.CgroupNSInode, comm: ev.Comm}]; ok {
		return
	}

	sb := d.lookupSandbox(ev)
	if sb == nil {
		// 非沙箱来源且非待评估对象，忽略。
		return
	}

	d.appendHistory(sb.ID, ev)

	var rule EscapeRule
	var reason string
	switch ev.Syscall {
	case SysMount:
		if isCgroupFSType(ev.FSType) && ev.Writable {
			rule = RuleWritableCgroupMount
			reason = "sandbox process mounted a writable cgroup filesystem at " + ev.MountPoint
		}
	case SysOpenat:
		if ev.Writable && ev.CgroupNSInode != 0 && d.isCgroupPath(ev) && path.Base(d.hostPath(ev)) == "notify_on_release" {
			rule = RuleNotifyOnRelease
			reason = "sandbox process opened notify_on_release for writing: " + d.hostPath(ev)
		}
	case SysWrite:
		base := path.Base(d.hostPath(ev))
		switch {
		case base == "release_agent" && d.isCgroupPath(ev):
			rule = RuleReleaseAgentWrite
			reason = "sandbox process wrote release_agent at " + d.hostPath(ev)
		case (base == "cgroup.procs" || base == "cgroup.threads") && d.isCgroupPath(ev):
			target := path.Dir(d.hostPath(ev))
			if !withinRoot(target, sb.HostCgroupRoot) {
				rule = RuleMigrateToParent
				reason = "sandbox process migrated a task outside its cgroup root to " + target
			}
		}
	case SysMigrate:
		target := cleanPath(d.hostPath(ev))
		if d.isCgroupPath(ev) && !withinRoot(target, sb.HostCgroupRoot) {
			rule = RuleMigrateToParent
			reason = "sandbox process migrated a task outside its cgroup root to " + target
		}
	}

	if rule == "" {
		return
	}

	alert := Alert{
		Rule:      rule,
		SandboxID: sb.ID,
		Reason:    reason,
		Time:      ev.Time,
		Trigger:   ev,
		Chain:     d.buildChain(sb.ID, ev),
	}
	if d.cfg.Block != nil {
		d.cfg.Block(alert)
	}
}

// lookupSandbox 依据 namespace inode 优先、宿主 cgroup 路径兜底判定归属。
func (d *Detector) lookupSandbox(ev *AuditEvent) *SandboxConfig {
	if ev.CgroupNSInode != 0 {
		if sb, ok := d.byNS[ev.CgroupNSInode]; ok {
			return sb
		}
	}
	if ev.HostCgroup != "" {
		hostCgroup := cleanPath(ev.HostCgroup)
		// 最长前缀匹配，避免兄弟沙箱互相误判。
		var best *SandboxConfig
		bestLen := -1
		for root, sb := range d.byRoot {
			if withinRoot(hostCgroup, root) && len(root) > bestLen {
				best, bestLen = sb, len(root)
			}
		}
		if best != nil {
			return best
		}
	}
	return nil
}

// appendHistory 记录沙箱事件并按窗口与上限裁剪。
func (d *Detector) appendHistory(sandboxID string, ev *AuditEvent) {
	items := d.history[sandboxID]
	cutoff := ev.Time.Add(-d.cfg.ChainWindow)
	kept := items[:0]
	for _, old := range items {
		if !old.Time.Before(cutoff) {
			kept = append(kept, old)
		}
	}
	kept = append(kept, ev)
	if len(kept) > d.cfg.ChainLimit {
		kept = kept[len(kept)-d.cfg.ChainLimit:]
	}
	d.history[sandboxID] = kept
}

// buildChain 还原触发事件所在的关联事件链。
func (d *Detector) buildChain(sandboxID string, trigger *AuditEvent) []*AuditEvent {
	cutoff := trigger.Time.Add(-d.cfg.ChainWindow)
	chain := make([]*AuditEvent, 0, len(d.history[sandboxID]))
	for _, old := range d.history[sandboxID] {
		if old.Time.Before(cutoff) || old.Time.After(trigger.Time) {
			continue
		}
		chain = append(chain, old)
	}
	sort.SliceStable(chain, func(i, j int) bool {
		if chain[i].Time.Equal(chain[j].Time) {
			return chain[i].Seq < chain[j].Seq
		}
		return chain[i].Time.Before(chain[j].Time)
	})
	return chain
}

// hostPath 返回事件的宿主机视角路径：优先使用采集器预解析结果，
// 否则通过内部挂载表把进程视角路径映射到宿主路径，最后回退原路径。
func (d *Detector) hostPath(ev *AuditEvent) string {
	if ev.ResolvedHostPath != "" {
		return cleanPath(ev.ResolvedHostPath)
	}
	p := cleanPath(ev.Path)
	var bestMount string
	for mp := range d.mounts {
		mp = cleanPath(mp)
		if (p == mp || strings.HasPrefix(p, mp+"/")) && len(mp) > len(bestMount) {
			bestMount = mp
		}
	}
	if bestMount != "" {
		info := d.mounts[bestMount]
		rel := strings.TrimPrefix(p, bestMount)
		return cleanPath(path.Join(cleanPath(info.root), rel))
	}
	return p
}

// isCgroupPath 判断事件目标是否位于 cgroup 文件系统，
// 命中挂载表时最可靠，否则用路径特征兜底。
func (d *Detector) isCgroupPath(ev *AuditEvent) bool {
	p := cleanPath(ev.Path)
	if ev.ResolvedHostPath != "" {
		p = cleanPath(ev.ResolvedHostPath)
	}
	for mp, info := range d.mounts {
		mp = cleanPath(mp)
		if isCgroupFSType(info.fsType) && (p == mp || strings.HasPrefix(p, mp+"/")) {
			return true
		}
	}
	return strings.Contains(p, "/cgroup")
}

func isCgroupFSType(fsType string) bool {
	return fsType == "cgroup" || fsType == "cgroup2"
}

func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	return path.Clean(p)
}

// withinRoot 判断 target 是否等于 root 或位于 root 子树内。
func withinRoot(target, root string) bool {
	target = cleanPath(target)
	root = cleanPath(root)
	if root == "/" || root == "." {
		return true
	}
	return target == root || strings.HasPrefix(target, root+"/")
}
