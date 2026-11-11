package netsec

import (
	"errors"
	"fmt"
)

var (
	// ErrNoCandidate 表示同一指纹下没有任何条目的合取允许该请求。
	ErrNoCandidate = errors.New("netsec: no entry permits this request")
	// ErrAmbiguous 表示多条目同时匹配且调用方未显式选择，已拒绝自动挑选。
	ErrAmbiguous = errors.New("netsec: multiple entries match; explicit selection required")
	// ErrCommandConflict 表示候选条目的强制命令互相冲突，禁止自动选择。
	ErrCommandConflict = errors.New("netsec: conflicting forced commands among candidates")
	// ErrUnknownSelection 表示显式选择的条目 ID 不在候选集合中。
	ErrUnknownSelection = errors.New("netsec: selected entry is not a candidate")
)

// Decision 是求值结果：被选中的完整条目及其证据。
type Decision struct {
	Evidence Evidence
}

// Candidates 返回同一公钥指纹下、各自合取独立允许该请求的所有条目证据。
// 只统计 Fingerprint 与给定指纹相等的条目；不同指纹的条目被忽略。
func Candidates(entries []Entry, fingerprint string, req Request) []Evidence {
	var out []Evidence
	for _, e := range entries {
		if e.Fingerprint != fingerprint {
			continue
		}
		if ev, ok := e.Allows(req); ok {
			out = append(out, ev)
		}
	}
	return out
}

// Evaluate 为会话宿主求值一次会话请求。
//
// 每个匹配条目独立形成合取；多条目同时匹配时绝不把不同条目的
// 选项拼接成更宽授权，只允许通过 pick 显式选择完整一条：
//   - pick 为空（自动选择）：仅当恰好一条候选时接受；
//     多条候选一律拒绝，且候选间强制命令冲突时返回 ErrCommandConflict。
//   - pick 非空：必须等于某条候选的 Entry.ID，整体选中该条目。
func Evaluate(entries []Entry, fingerprint string, req Request, pick string) (Decision, error) {
	cands := Candidates(entries, fingerprint, req)
	if len(cands) == 0 {
		return Decision{}, ErrNoCandidate
	}

	if pick == "" {
		if len(cands) > 1 {
			if commandsConflict(cands) {
				return Decision{}, fmt.Errorf("%w: %w", ErrAmbiguous, ErrCommandConflict)
			}
			return Decision{}, ErrAmbiguous
		}
		return Decision{Evidence: cands[0]}, nil
	}

	for _, ev := range cands {
		if ev.EntryID == pick {
			return Decision{Evidence: ev}, nil
		}
	}
	return Decision{}, fmt.Errorf("%w: %q", ErrUnknownSelection, pick)
}

func commandsConflict(cands []Evidence) bool {
	first := cands[0].ForcedCommand
	for _, ev := range cands[1:] {
		if ev.ForcedCommand != first {
			return true
		}
	}
	return false
}
