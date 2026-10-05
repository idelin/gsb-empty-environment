package delegation

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"
)

// record 是某授权某一代次的内部记录，全部状态变迁都带时间戳，
// 因此任意历史时点的状态都可以重建。
type record struct {
	grant        Grant
	generation   int
	issuedAt     time.Time
	revokedAt    *time.Time
	supersededAt *time.Time
}

func (r *record) ref() Ref { return Ref{ID: r.grant.ID, Generation: r.generation} }

func keyOf(id string, generation int) string {
	return id + "#" + strconv.Itoa(generation)
}

// Store 是授权委托图。它不是并发安全的；如需并发访问请自行加锁。
type Store struct {
	byID map[string][]*record // ID -> 按代次升序的记录
}

// NewStore 创建一个空的委托图。
func NewStore() *Store {
	return &Store{byID: make(map[string][]*record)}
}

// Issue 在 at 时刻签发一份授权，返回其代次。
// 同一 ID 重复签发会生成新的代次，并在 at 时刻取代上一代。
// 签发时不强制父链存在；父链缺失会在校验时被报告。
func (s *Store) Issue(g Grant, at time.Time) (int, error) {
	if g.ID == "" {
		return 0, errors.New("delegation: grant ID must not be empty")
	}
	if g.NotAfter.Before(g.NotBefore) {
		return 0, fmt.Errorf("delegation: grant %q has NotAfter before NotBefore", g.ID)
	}
	recs := s.byID[g.ID]
	generation := 1
	if n := len(recs); n > 0 {
		prev := recs[n-1]
		generation = prev.generation + 1
		if at.Before(prev.issuedAt) {
			return 0, fmt.Errorf("delegation: cannot issue %q at %v before previous generation issued at %v",
				g.ID, at, prev.issuedAt)
		}
		t := at
		prev.supersededAt = &t
	}
	rec := &record{grant: g, generation: generation, issuedAt: at}
	s.byID[g.ID] = append(recs, rec)
	return generation, nil
}

// Revoke 在 at 时刻撤销指定 ID 的授权。
// generation <= 0 表示撤销该 ID 的全部代次；否则只撤销指定代次。
// 撤销会立即使该授权及其全部后代失效（对 at 及之后的时点查询而言）。
func (s *Store) Revoke(id string, generation int, at time.Time) error {
	recs := s.byID[id]
	if len(recs) == 0 {
		return fmt.Errorf("delegation: cannot revoke unknown grant %q", id)
	}
	revoked := false
	for _, rec := range recs {
		if generation > 0 && rec.generation != generation {
			continue
		}
		if rec.revokedAt == nil || at.Before(*rec.revokedAt) {
			t := at
			rec.revokedAt = &t
		}
		revoked = true
	}
	if !revoked {
		return fmt.Errorf("delegation: grant %q has no generation %d", id, generation)
	}
	return nil
}

// Lookup 返回指定 ID 在 at 时刻已签发的最新代次。
// 若该时刻尚未签发任何代次，返回 false。
func (s *Store) Lookup(id string, at time.Time) (Ref, bool) {
	rec := s.latest(id, at)
	if rec == nil {
		return Ref{}, false
	}
	return rec.ref(), true
}

// latest 返回 at 时刻已签发的最新一代；at 为零值时返回绝对最新一代。
func (s *Store) latest(id string, at time.Time) *record {
	recs := s.byID[id]
	if len(recs) == 0 {
		return nil
	}
	if at.IsZero() {
		return recs[len(recs)-1]
	}
	// recs 按签发时间升序（Issue 保证代次与时间同序）。
	i := sort.Search(len(recs), func(i int) bool { return recs[i].issuedAt.After(at) })
	if i == 0 {
		return nil
	}
	return recs[i-1]
}

// resolve 按引用解析记录：generation <= 0 时解析到 at 时刻最新一代。
func (s *Store) resolve(ref Ref, at time.Time) *record {
	if ref.Generation > 0 {
		for _, rec := range s.byID[ref.ID] {
			if rec.generation == ref.Generation {
				return rec
			}
		}
		return nil
	}
	return s.latest(ref.ID, at)
}

// resolveParent 解析子授权的父引用。
// 父引用未指定代次时，解析到子授权签发时刻父授权的最新一代，
// 使“同名重发”后旧子授权仍然指向它签发时所依托的那一代。
func (s *Store) resolveParent(rec *record) *record {
	p := rec.grant.Parent
	if p == nil {
		return nil
	}
	if p.Generation > 0 {
		return s.resolve(*p, time.Time{})
	}
	return s.latest(p.ID, rec.issuedAt)
}
