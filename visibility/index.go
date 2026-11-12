// Package visibility 实现版本化的倒排索引可见性控制。
//
// 安全模型：查询服务只能把“调用方被授权、且在查询固定的索引根上仍然存活”
// 的当前代文档返回给调用方。索引中的每一项都带文档代次（gen）与单调版本号，
// 删除与替换写入墓碑（tombstone）而不是原地擦除，从而让旧索引根在重建后仍能
// 复现“该文档已不存在”的结论，避免删除残留泄漏历史存在性。
//
// 一个 Root 是某一时刻索引的不可变只读视图，一个 Snapshot 是某一时刻授权
// 集合的不可变只读视图。Index.Query 同时固定根与快照，只输出两者交集内、在
// 该根上为当前代的存活文档。
//
// 墓碑回收（压缩）规则：墓碑只有在“最旧的仍被持有索引根”已经观察到该墓碑
// （rmin >= tombstone.version）时才允许丢弃；此时该墓碑所遮蔽的旧代 postings
// 也一并丢弃。仍被任何已持有旧根需要的墓碑必须保留，Index.Compact 严格执行
// 该不变量。
package visibility

import (
	"errors"
	"sort"
	"sync"
)

// Version 是索引的单调逻辑版本，每次写入/删除严格递增；压缩只是物理重写，
// 不产生新的逻辑版本（任何已持有根看到的视图保持不变）。
type Version int64

// DocID 是文档标识。
type DocID string

// Gen 是文档代次。同一 DocID 每次写入（含删除后重建、替换）都取得新的代次，
// 代次单调递增、永不复用。
type Gen int64

// kind 区分日志记录类型。
type kind uint8

const (
	kindAdd kind = iota + 1
	kindTombstone
)

// record 是仅追加的变更日志记录。
type record struct {
	version Version
	kind    kind
	doc     DocID
	gen     Gen
	terms   []string // 仅 kindAdd 使用
}

// posting 是倒排表中的一个版本化索引项。
type posting struct {
	version Version
	doc     DocID
	gen     Gen
	tomb    bool // true 表示删除墓碑，false 表示存活索引项
}

// docState 记录每个文档的最新状态与当前代次。
type docState struct {
	gen   Gen
	terms []string
	alive bool
}

// Index 是并发安全的版本化倒排索引。
type Index struct {
	mu sync.RWMutex

	version Version
	docs    map[DocID]*docState
	log     []record
	index   map[string][]posting // term -> 按版本递增的索引项

	// 已持有索引根集合（按版本引用计数）。压缩下界 rmin 取其中最小版本；
	// 集合为空时 rmin 取当前版本（没有任何旧视图需要历史项）。
	roots map[Version]int
}

// New 创建空索引。
func New() *Index {
	return &Index{
		docs:  make(map[DocID]*docState),
		index: make(map[string][]posting),
		roots: make(map[Version]int),
	}
}

// Root 是索引在某个版本上的不可变只读句柄。Root 必须在用完后调用 Release，
// 否则压缩会一直保守地保留该根所需的全部历史项与墓碑。
type Root struct {
	index    *Index
	version  Version
	released bool
}

// Version 返回该根固定的索引版本。
func (r *Root) Version() Version { return r.version }

// ErrRootReleased 在查询已释放（或非本索引）的根时返回。
var ErrRootReleased = errors.New("visibility: index root has been released")

// ErrSnapshotReleased 在查询已释放（或非本授权器）的快照时返回。
var ErrSnapshotReleased = errors.New("visibility: authorization snapshot has been released")

// Head 返回当前版本的索引根并登记持有。初始空索引的根版本为 0。
func (ix *Index) Head() *Root {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return ix.retainRootLocked(ix.version)
}

func (ix *Index) retainRootLocked(v Version) *Root {
	ix.roots[v]++
	return &Root{index: ix, version: v}
}

// Release 释放根持有的版本引用。重复释放返回错误而不是静默成功。
func (r *Root) Release() error {
	if r == nil || r.index == nil || r.released {
		return ErrRootReleased
	}
	ix := r.index
	ix.mu.Lock()
	defer ix.mu.Unlock()
	n := ix.roots[r.version]
	if n <= 0 {
		r.released = true
		return ErrRootReleased
	}
	if n == 1 {
		delete(ix.roots, r.version)
	} else {
		ix.roots[r.version] = n - 1
	}
	r.released = true
	return nil
}

func (ix *Index) checkRootLocked(r *Root) error {
	if r == nil || r.index != ix || r.released || ix.roots[r.version] == 0 {
		return ErrRootReleased
	}
	return nil
}

// rminLocked 返回墓碑/历史项回收的安全下界：最旧的仍被持有根的版本。
func (ix *Index) rminLocked() Version {
	if len(ix.roots) == 0 {
		return ix.version
	}
	first := true
	var min Version
	for v := range ix.roots {
		if first || v < min {
			min, first = v, false
		}
	}
	return min
}

// Put 写入（或替换、重建）文档，返回新的索引根。文档取得全新的、不复用的
// 代次；若旧代仍存活，会先为旧代写墓碑。terms 会被去重并按字典序规范化。
func (ix *Index) Put(doc DocID, terms []string) *Root {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	ix.version++
	norm := normalizeTerms(terms)

	if st, ok := ix.docs[doc]; ok && st.alive {
		oldGen := st.gen
		ix.log = append(ix.log, record{
			version: ix.version, kind: kindTombstone, doc: doc, gen: oldGen,
		})
		for _, term := range st.terms {
			ix.index[term] = append(ix.index[term], posting{
				version: ix.version, doc: doc, gen: oldGen, tomb: true,
			})
		}
	}

	var newGen Gen
	if st, ok := ix.docs[doc]; ok {
		newGen = st.gen + 1
	} else {
		newGen = 1
	}
	ix.log = append(ix.log, record{
		version: ix.version, kind: kindAdd, doc: doc, gen: newGen, terms: norm,
	})
	for _, term := range norm {
		ix.index[term] = append(ix.index[term], posting{
			version: ix.version, doc: doc, gen: newGen, tomb: false,
		})
	}

	if st, ok := ix.docs[doc]; ok {
		st.gen, st.terms, st.alive = newGen, norm, true
	} else {
		ix.docs[doc] = &docState{gen: newGen, terms: norm, alive: true}
	}
	return ix.retainRootLocked(ix.version)
}

// Delete 为文档的当前代写墓碑，返回新的索引根。对不存在或已删除的文档
// 调用是空操作（仍返回当前根）。
func (ix *Index) Delete(doc DocID) *Root {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	if st, ok := ix.docs[doc]; ok && st.alive {
		ix.version++
		ix.log = append(ix.log, record{
			version: ix.version, kind: kindTombstone, doc: doc, gen: st.gen,
		})
		for _, term := range st.terms {
			ix.index[term] = append(ix.index[term], posting{
				version: ix.version, doc: doc, gen: st.gen, tomb: true,
			})
		}
		st.alive = false
	}
	return ix.retainRootLocked(ix.version)
}

// Compact 在不改变任何已持有根查询结果的前提下重写倒排表，回收可安全丢弃的
// 历史索引项与墓碑。
//
// 设 rmin 为最旧的仍被持有根的版本：
//   - 版本号 > rmin 的索引项原样保留（至少有一个已持有根还看不到它，该根的
//     视图必须能重放出这些变更）；
//   - 版本号 <= rmin 的索引项按 (term, doc) 折叠：取每个文档在边界内的最新
//     一项。若它是存活项，它代表 rmin 时刻的当前代；若它是墓碑，则该文档在
//     边界内的全部项（含墓碑本身与被它遮蔽的存活项）一起丢弃——因为所有仍被
//     持有的根都已观察到删除，墓碑已无重放价值。边界外可能存在的同文档新代
//     存活项属于后缀，会被完整保留，不依赖边界内的墓碑；
//   - 版本号 > rmin 的墓碑与存活项全部原样保留——这正是“压缩不得丢掉仍被旧
//     快照需要的墓碑”的强制点。
func (ix *Index) Compact() {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	rmin := ix.rminLocked()
	compacted := make(map[string][]posting, len(ix.index))

	for term, postings := range ix.index {
		kept := make([]posting, 0, len(postings))

		// 折叠 <= rmin 的前缀：取每个文档最新（版本最大）的一项；若最新项
		// 是墓碑，则该文档在边界内的所有项都可丢弃。
		folded := make(map[DocID]posting)
		i := 0
		for i < len(postings) && postings[i].version <= rmin {
			folded[postings[i].doc] = postings[i]
			i++
		}

		docs := make([]DocID, 0, len(folded))
		for doc := range folded {
			docs = append(docs, doc)
		}
		sort.Slice(docs, func(a, b int) bool { return docs[a] < docs[b] })
		for _, doc := range docs {
			if p := folded[doc]; !p.tomb {
				kept = append(kept, p)
			}
		}

		// 后缀（版本 > rmin）：墓碑与存活项都原样保留，顺序不变。
		kept = append(kept, postings[i:]...)

		if len(kept) > 0 {
			compacted[term] = kept
		}
	}

	ix.index = compacted

	// 重写变更日志：丢弃所有已持有根都已观察到（version <= rmin）的记录，
	// 使日志与压缩后的倒排表保持同一回收边界。
	// 必须分配新切片：原地复用底层数组会在 append 时覆盖尚未遍历到的记录。
	filtered := make([]record, 0, len(ix.log))
	for _, rec := range ix.log {
		if rec.version > rmin {
			filtered = append(filtered, rec)
		}
	}
	ix.log = filtered
}

// DocResult 是查询命中的一个当前代文档。
type DocResult struct {
	Doc DocID
	Gen Gen
}

// Query 固定索引根 root 与授权快照 snap，返回同时满足以下条件的文档：
//   - 包含 terms 中全部词项（AND 语义；空 terms 不命中任何文档）；
//   - 在 root 上存活，且所观察到的是该文档在 root 上的当前代；
//   - 在 snap 中被授权。
//
// 结果按 DocID 排序。已删除文档、被撤回授权的文档都不会出现在结果中，
// 调用方无法通过本接口区分“不存在 / 已删除 / 未授权”，从而不泄漏历史存在性。
func (ix *Index) Query(root *Root, snap *Snapshot, terms []string) ([]DocResult, error) {
	if len(terms) == 0 {
		return nil, nil
	}

	ix.mu.RLock()
	defer ix.mu.RUnlock()
	if err := ix.checkRootLocked(root); err != nil {
		return nil, err
	}
	allowed, err := snap.validate()
	if err != nil {
		return nil, err
	}

	uniq := normalizeTerms(terms)

	type hit struct{ gen Gen }
	var candidates map[DocID]hit

	for _, term := range uniq {
		current := make(map[DocID]hit)
		for _, p := range ix.index[term] {
			if p.version > root.version {
				break // postings 按版本递增，后续都属于更晚的根
			}
			if p.tomb {
				delete(current, p.doc)
			} else {
				current[p.doc] = hit{gen: p.gen}
			}
		}
		if candidates == nil {
			candidates = current
			continue
		}
		for doc := range candidates {
			if _, ok := current[doc]; !ok {
				delete(candidates, doc)
			}
		}
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	docs := make([]DocID, 0, len(candidates))
	for doc := range candidates {
		if _, ok := allowed[doc]; ok {
			docs = append(docs, doc)
		}
	}
	sort.Slice(docs, func(a, b int) bool { return docs[a] < docs[b] })

	out := make([]DocResult, 0, len(docs))
	for _, doc := range docs {
		out = append(out, DocResult{Doc: doc, Gen: candidates[doc].gen})
	}
	return out, nil
}

func normalizeTerms(terms []string) []string {
	if len(terms) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(terms))
	out := make([]string, 0, len(terms))
	for _, term := range terms {
		if term == "" {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		out = append(out, term)
	}
	sort.Strings(out)
	return out
}
