package netsec

import (
	"fmt"
	"sort"
	"sync"
)

// resourceEntry 是索引内部的资源记录。
type resourceEntry struct {
	id        string
	excDeps   []ExceptionVersion
	upstreams []string

	restricted bool
	policy     TerminationPolicy
	causes     []ExceptionVersion

	// runningOps 仅保留尚未终止的在途操作，便于失效时统一处理。
	runningOps map[string]*Operation
}

// DependencyIndex 是临时安全例外的依赖索引。
//
// 它维护两类图边：
//   - 资源 -> 例外版本集合（直接依赖，按世代钉住）；
//   - 资源 -> 上游资源（传递依赖，失效沿边向下游传播）。
//
// 撤销或到期不会删除根记录，而是写入失效墓碑并把整条依赖链
// 切换到受限状态，同时按终止策略处理已经开始的敏感操作。
type DependencyIndex struct {
	mu    sync.RWMutex
	clock Clock

	// exceptions 指向每个 ID 的当前世代对象。
	exceptions map[string]*Exception
	// exceptionGens 保留全部世代，旧世代重建后仍可解释历史依赖。
	exceptionGens map[string]map[int]*Exception
	resources     map[string]*resourceEntry

	operations map[string]*Operation
	opSeq      int

	// events 是追加写的失效墓碑列表。
	events []InvalidationEvent
}

// NewDependencyIndex 创建依赖索引，clock 为 nil 时使用 SystemClock。
func NewDependencyIndex(clock Clock) *DependencyIndex {
	if clock == nil {
		clock = SystemClock{}
	}
	return &DependencyIndex{
		clock:         clock,
		exceptions:    make(map[string]*Exception),
		exceptionGens: make(map[string]map[int]*Exception),
		resources:     make(map[string]*resourceEntry),
		operations:    make(map[string]*Operation),
	}
}

// RegisterException 注册业务 ID 为 id 的临时安全例外。
//
// 首次注册世代为 1；若同 ID 例外已失效（墓碑保留），再次注册
// 会创建递增的新世代，旧世代依赖不会自动迁移。同 ID 仍有效时
// 返回 ErrExceptionExists。
func (idx *DependencyIndex) RegisterException(id string, expiresAt Time, policy TerminationPolicy) (*Exception, error) {
	if id == "" {
		return nil, ErrEmptyID
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if current := idx.exceptions[id]; current != nil && current.Active() {
		return nil, fmt.Errorf("%w: %s", ErrExceptionExists, id)
	}
	generation := 1
	if gens := idx.exceptionGens[id]; len(gens) > 0 {
		generation = len(gens) + 1
	}
	exc := &Exception{
		ID:         id,
		Generation: generation,
		ExpiresAt:  expiresAt,
		Policy:     policy,
		state:      ExceptionActive,
	}
	idx.exceptions[id] = exc
	if idx.exceptionGens[id] == nil {
		idx.exceptionGens[id] = make(map[int]*Exception)
	}
	idx.exceptionGens[id][generation] = exc
	return cloneException(exc), nil
}

// Exception 返回 ID 当前世代的例外快照；不存在返回 nil。
func (idx *DependencyIndex) Exception(id string) *Exception {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if exc := idx.exceptions[id]; exc != nil {
		return cloneException(exc)
	}
	return nil
}

// ExceptionGeneration 返回指定世代的例外（含已失效墓碑）；不存在返回 nil。
func (idx *DependencyIndex) ExceptionGeneration(v ExceptionVersion) *Exception {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if gens := idx.exceptionGens[v.ID]; gens != nil {
		if exc := gens[v.Generation]; exc != nil {
			return cloneException(exc)
		}
	}
	return nil
}

// RegisterResource 注册资源并声明其直接例外依赖与上游资源。
//
// 例外依赖按 ExceptionVersion 精确钉住世代；上游必须存在且未受限，
// 依赖图不允许成环。注册成功后资源立即按当前索引状态计算受限性。
func (idx *DependencyIndex) RegisterResource(id string, excDeps []ExceptionVersion, upstreams []string) (*Resource, error) {
	if id == "" {
		return nil, ErrEmptyID
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if _, exists := idx.resources[id]; exists {
		return nil, fmt.Errorf("%w: %s", ErrResourceExists, id)
	}
	excSet, err := idx.validateExceptionDeps(excDeps)
	if err != nil {
		return nil, err
	}
	upSet, err := idx.validateUpstreams(id, upstreams)
	if err != nil {
		return nil, err
	}
	entry := &resourceEntry{
		id:         id,
		excDeps:    excSet,
		upstreams:  upSet,
		runningOps: make(map[string]*Operation),
	}
	idx.resources[id] = entry
	idx.recomputeAllLocked()
	return idx.snapshotResourceLocked(entry), nil
}

// RebaseResource 替换资源的依赖集合（例外版本与上游），随后全量
// 重算受限状态。它是资源迁移到新世代例外的显式手段：仅注册新
// 例外不会改变仍钉在旧世代上的资源。
func (idx *DependencyIndex) RebaseResource(id string, excDeps []ExceptionVersion, upstreams []string) (*Resource, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	entry, ok := idx.resources[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrResourceNotFound, id)
	}
	excSet, err := idx.validateExceptionDeps(excDeps)
	if err != nil {
		return nil, err
	}
	upSet, err := idx.validateUpstreams(id, upstreams)
	if err != nil {
		return nil, err
	}
	entry.excDeps = excSet
	entry.upstreams = upSet
	idx.recomputeAllLocked()
	return idx.snapshotResourceLocked(entry), nil
}

// Resource 返回资源的依赖与受限状态快照；不存在返回 nil。
func (idx *DependencyIndex) Resource(id string) *Resource {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if entry := idx.resources[id]; entry != nil {
		return idx.snapshotResourceLocked(entry)
	}
	return nil
}

// Events 返回全部失效墓碑的副本。
func (idx *DependencyIndex) Events() []InvalidationEvent {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make([]InvalidationEvent, len(idx.events))
	copy(out, idx.events)
	return out
}

// validateExceptionDeps 校验例外依赖集合：去重排序，且每个版本
// 必须存在并处于有效状态（禁止在已失效墓碑上建立新资源）。
func (idx *DependencyIndex) validateExceptionDeps(deps []ExceptionVersion) ([]ExceptionVersion, error) {
	set := make([]ExceptionVersion, 0, len(deps))
	for _, v := range deps {
		if v.ID == "" {
			return nil, ErrEmptyID
		}
		gens := idx.exceptionGens[v.ID]
		if gens == nil || gens[v.Generation] == nil {
			return nil, fmt.Errorf("%w: %s", ErrExceptionNotFound, v)
		}
		if !gens[v.Generation].Active() {
			return nil, fmt.Errorf("%w: %s", ErrDependencyInactive, v)
		}
		if !containsVersion(set, v) {
			set = append(set, v)
		}
	}
	sort.Slice(set, func(i, j int) bool { return versionLess(set[i], set[j]) })
	return set, nil
}

// validateUpstreams 校验上游集合：去重排序、必须存在、当前未受限，
// 且新边不会引入环。
func (idx *DependencyIndex) validateUpstreams(selfID string, upstreams []string) ([]string, error) {
	set := make([]string, 0, len(upstreams))
	for _, up := range upstreams {
		if up == "" {
			return nil, ErrEmptyID
		}
		if up == selfID {
			return nil, fmt.Errorf("%w: %s -> %s", ErrDependencyCycle, selfID, up)
		}
		entry := idx.resources[up]
		if entry == nil {
			return nil, fmt.Errorf("%w: %s", ErrUpstreamNotFound, up)
		}
		if entry.restricted {
			return nil, fmt.Errorf("%w: %s", ErrUpstreamInactive, up)
		}
		if !containsString(set, up) {
			set = append(set, up)
		}
	}
	sort.Strings(set)
	if idx.createsCycleLocked(selfID, set) {
		return nil, fmt.Errorf("%w: at resource %s", ErrDependencyCycle, selfID)
	}
	return set, nil
}

// createsCycleLocked 在既有 DAG 上检查 selfID 指向 newUpstreams 后
// 是否成环：若任一上游能沿既有上游边回溯到 selfID，则成环。
func (idx *DependencyIndex) createsCycleLocked(selfID string, newUpstreams []string) bool {
	for _, root := range newUpstreams {
		stack := []string{root}
		seen := map[string]bool{}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if cur == selfID {
				return true
			}
			if seen[cur] {
				continue
			}
			seen[cur] = true
			if entry := idx.resources[cur]; entry != nil {
				stack = append(stack, entry.upstreams...)
			}
		}
	}
	return false
}
