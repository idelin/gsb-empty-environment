// Package release 提供跨区域安全策略的两阶段发布组件。
//
// 语义约定：
//   - 版本绑定区域集合与策略摘要，回执必须匹配版本号与摘要，否则视为旧版回执。
//   - 准备阶段任一区域失败：全局撤回（Abort），状态为 Aborted。
//   - 提交阶段任一区域失败：只能报告部分生效（PartiallyEffective）并持续收敛，
//     绝不宣称已回滚。
//   - 控制器重启后通过持久化状态恢复：未进入提交阶段可安全撤回；
//     已进入提交阶段只能继续向前收敛。
package release

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Phase 描述一次发布的全局状态。
type Phase string

const (
	PhaseIdle               Phase = "idle"
	PhasePreparing          Phase = "preparing"
	PhasePrepared           Phase = "prepared"
	PhaseCommitting         Phase = "committing"
	PhaseEffective          Phase = "effective"
	PhasePartiallyEffective Phase = "partially_effective"
	PhaseAborted            Phase = "aborted"
)

// Terminal 报告状态是否已终结（不再变化）。
func (p Phase) Terminal() bool {
	return p == PhaseEffective || p == PhaseAborted
}

// Version 是待发布的策略版本，绑定区域集合与策略摘要。
type Version struct {
	ID      string   // 版本号，单调递增由宿主保证
	Digest  string   // 策略内容摘要
	Regions []string // 目标区域集合
}

// Validate 校验版本定义是否完整。
func (v Version) Validate() error {
	if v.ID == "" {
		return errors.New("release: version ID is empty")
	}
	if v.Digest == "" {
		return errors.New("release: policy digest is empty")
	}
	if len(v.Regions) == 0 {
		return errors.New("release: region set is empty")
	}
	seen := make(map[string]bool, len(v.Regions))
	for _, r := range v.Regions {
		if r == "" {
			return errors.New("release: region name is empty")
		}
		if seen[r] {
			return fmt.Errorf("release: duplicate region %q", r)
		}
		seen[r] = true
	}
	return nil
}

// Receipt 是区域代理对某次操作的确认回执。
type Receipt struct {
	Region    string
	VersionID string // 回执所针对的版本；与当前版本不一致即为旧版回执
	Digest    string // 区域侧实际装载的策略摘要
	Seq       uint64 // 区域侧单调序号，用于识别重复确认
}

// Agent 是单个区域的受控代理。所有操作必须幂等：
// 对同一版本的重复调用应返回相同回执且不产生副作用。
type Agent interface {
	Prepare(ctx context.Context, v Version) (Receipt, error)
	Commit(ctx context.Context, versionID string) (Receipt, error)
	Abort(ctx context.Context, versionID string) (Receipt, error)
}

// 回执校验错误。
var (
	ErrStaleReceipt   = errors.New("release: stale receipt (version or digest mismatch)")
	ErrUnknownRegion  = errors.New("release: receipt from unknown region")
	ErrCommitInFlight = errors.New("release: commit phase already started, rollback is impossible")
)

// State 是持久化的发布状态，控制器重启后据此恢复。
type State struct {
	Version       Version
	Phase         Phase
	CommitStarted bool // 一旦为 true，全局撤回不再合法
	Prepared      map[string]bool
	Committed     map[string]bool
}

func (s *State) clone() *State {
	if s == nil {
		return nil
	}
	out := *s
	out.Version.Regions = append([]string(nil), s.Version.Regions...)
	out.Prepared = make(map[string]bool, len(s.Prepared))
	for k, v := range s.Prepared {
		out.Prepared[k] = v
	}
	out.Committed = make(map[string]bool, len(s.Committed))
	for k, v := range s.Committed {
		out.Committed[k] = v
	}
	return &out
}

// Report 是发布/收敛结果的对外报告。
type Report struct {
	VersionID string
	Phase     Phase
	Prepared  []string // 已完成准备的区域
	Committed []string // 已生效的区域
	Pending   []string // 尚未生效、待收敛的区域
	Aborted   bool     // 全局已撤回（仅准备阶段失败才可能出现）
	LastError string   // 最近一次区域错误，便于宿主告警
}

// Effective 报告全局是否已完全生效。
func (r Report) Effective() bool { return r.Phase == PhaseEffective }

// Partial 报告是否处于部分生效（仍在收敛）。
func (r Report) Partial() bool { return r.Phase == PhasePartiallyEffective }

// Controller 编排一次发布：先全部准备，再全部提交。
// 状态通过 Store 持久化，进程重启后由 Recover 恢复。
type Controller struct {
	mu     sync.Mutex
	agents map[string]Agent
	store  Store
	st     *State
}

// NewController 创建控制器。agents 按区域名索引。
func NewController(store Store, agents map[string]Agent) *Controller {
	return &Controller{agents: agents, store: store}
}

// Release 执行一次完整发布。返回值报告终态或部分生效态；
// 仅在参数错误、存储错误或 ctx 取消时返回非 nil error。
func (c *Controller) Release(ctx context.Context, v Version) (Report, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := v.Validate(); err != nil {
		return Report{}, err
	}
	for _, r := range v.Regions {
		if _, ok := c.agents[r]; !ok {
			return Report{}, fmt.Errorf("release: no agent for region %q", r)
		}
	}

	c.st = &State{
		Version:   v,
		Phase:     PhasePreparing,
		Prepared:  make(map[string]bool, len(v.Regions)),
		Committed: make(map[string]bool, len(v.Regions)),
	}
	if err := c.save(ctx); err != nil {
		return Report{}, err
	}

	// 阶段一：全部区域准备。任一失败或回执非法 -> 全局撤回。
	for _, region := range v.Regions {
		rc, err := c.agents[region].Prepare(ctx, v)
		if err != nil {
			if ctx.Err() != nil {
				// 宿主取消：保持现场，交由 Recover 决策。
				return c.reportLocked(err), ctx.Err()
			}
			c.abortAllLocked(ctx)
			return c.reportLocked(err), nil
		}
		if err := c.checkReceiptLocked(rc); err != nil {
			c.abortAllLocked(ctx)
			return c.reportLocked(err), nil
		}
		c.st.Prepared[region] = true
		if err := c.save(ctx); err != nil {
			return Report{}, err
		}
	}
	c.st.Phase = PhasePrepared
	if err := c.save(ctx); err != nil {
		return Report{}, err
	}

	// 阶段二：提交。从此刻起只能向前收敛，不能宣称回滚。
	c.st.Phase = PhaseCommitting
	c.st.CommitStarted = true
	if err := c.save(ctx); err != nil {
		return Report{}, err
	}
	var firstErr error
	for _, region := range v.Regions {
		if err := c.commitRegionLocked(ctx, region); err != nil && firstErr == nil {
			firstErr = err
		}
		if ctx.Err() != nil {
			return c.reportLocked(firstErr), ctx.Err()
		}
	}
	c.finishCommitPassLocked(ctx)
	return c.reportLocked(firstErr), nil
}

// Converge 对处于部分生效状态的发布执行一轮收敛：
// 仅重试尚未提交的区域，已成功区域不会重复提交。
// 宿主应周期性调用直至报告 Effective。
func (c *Controller) Converge(ctx context.Context) (Report, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.st == nil || c.st.Phase != PhasePartiallyEffective {
		return c.reportLocked(nil), nil
	}
	var firstErr error
	for _, region := range c.st.Version.Regions {
		if c.st.Committed[region] {
			continue
		}
		if err := c.commitRegionLocked(ctx, region); err != nil && firstErr == nil {
			firstErr = err
		}
		if ctx.Err() != nil {
			return c.reportLocked(firstErr), ctx.Err()
		}
	}
	c.finishCommitPassLocked(ctx)
	return c.reportLocked(firstErr), nil
}

// Recover 在控制器重启后恢复发布状态。
// 未进入提交阶段的中断可安全全局撤回；已进入提交阶段只能继续收敛。
func (c *Controller) Recover(ctx context.Context) (Report, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	st, err := c.store.Load(ctx)
	if errors.Is(err, ErrNoState) {
		c.st = nil
		return Report{Phase: PhaseIdle}, nil
	}
	if err != nil {
		return Report{}, err
	}
	c.st = st
	if !st.CommitStarted {
		// 提交尚未开始，撤回是安全的。
		c.abortAllLocked(ctx)
		return c.reportLocked(nil), nil
	}
	// 提交已开始：校正状态并等待 Converge 继续推进。
	c.finishCommitPassLocked(ctx)
	return c.reportLocked(nil), nil
}

// Snapshot 返回当前状态报告的只读副本。
func (c *Controller) Snapshot() Report {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reportLocked(nil)
}

// commitRegionLocked 提交单个区域，幂等：已提交区域直接跳过。
func (c *Controller) commitRegionLocked(ctx context.Context, region string) error {
	if c.st.Committed[region] {
		return nil
	}
	rc, err := c.agents[region].Commit(ctx, c.st.Version.ID)
	if err != nil {
		return err
	}
	if err := c.checkReceiptLocked(rc); err != nil {
		return err
	}
	c.st.Committed[region] = true
	return c.save(ctx)
}

// finishCommitPassLocked 在一轮提交后校正全局状态。
// 不变量：进入提交阶段后绝不置为 Aborted。
func (c *Controller) finishCommitPassLocked(ctx context.Context) {
	if len(c.st.Committed) == len(c.st.Version.Regions) {
		c.st.Phase = PhaseEffective
	} else {
		c.st.Phase = PhasePartiallyEffective
	}
	_ = c.save(ctx)
}

// abortAllLocked 全局撤回。仅在提交阶段开始前合法。
func (c *Controller) abortAllLocked(ctx context.Context) {
	if c.st.CommitStarted {
		// 防御性兜底：提交后声称回滚是协议违规。
		return
	}
	for _, region := range c.st.Version.Regions {
		_, _ = c.agents[region].Abort(ctx, c.st.Version.ID) // 尽力而为
	}
	c.st.Phase = PhaseAborted
	_ = c.save(ctx)
}

// checkReceiptLocked 校验回执：拒绝旧版回执与未知区域回执。
// 重复确认由 Prepared/Committed 集合天然去重。
func (c *Controller) checkReceiptLocked(rc Receipt) error {
	if rc.VersionID != c.st.Version.ID || rc.Digest != c.st.Version.Digest {
		return fmt.Errorf("%w: region=%s version=%s", ErrStaleReceipt, rc.Region, rc.VersionID)
	}
	if !contains(c.st.Version.Regions, rc.Region) {
		return fmt.Errorf("%w: %s", ErrUnknownRegion, rc.Region)
	}
	return nil
}

func (c *Controller) reportLocked(lastErr error) Report {
	if c.st == nil {
		return Report{Phase: PhaseIdle}
	}
	r := Report{
		VersionID: c.st.Version.ID,
		Phase:     c.st.Phase,
		Aborted:   c.st.Phase == PhaseAborted,
	}
	for _, region := range c.st.Version.Regions {
		if c.st.Prepared[region] {
			r.Prepared = append(r.Prepared, region)
		}
		if c.st.Committed[region] {
			r.Committed = append(r.Committed, region)
		} else if c.st.CommitStarted {
			r.Pending = append(r.Pending, region)
		}
	}
	sort.Strings(r.Prepared)
	sort.Strings(r.Committed)
	sort.Strings(r.Pending)
	if lastErr != nil {
		r.LastError = lastErr.Error()
	}
	return r
}

func (c *Controller) save(ctx context.Context) error {
	return c.store.Save(ctx, c.st)
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
