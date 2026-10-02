package skewguard

import (
	"sort"
	"strings"
)

// KV 是一个有序的键值对，由 Scan 返回。
type KV struct {
	Key   string
	Value string
}

// View 是谓词求值时使用的只读视图。
// 事务执行期间它基于「事务快照 + 自身未提交写」，
// 提交重验时它基于「最新已提交状态 + 本事务待提交写」。
type View interface {
	Get(key string) (string, bool)
	Scan(prefix string) []KV
}

// overlayView 将一组待定写（nil 表示删除）叠加到底层状态之上。
type overlayView struct {
	base   map[string]string
	writes map[string]*string
}

func (v overlayView) Get(key string) (string, bool) {
	if w, ok := v.writes[key]; ok {
		if w == nil {
			return "", false
		}
		return *w, true
	}
	val, ok := v.base[key]
	return val, ok
}

func (v overlayView) Scan(prefix string) []KV {
	merged := make(map[string]string)
	for k, val := range v.base {
		if strings.HasPrefix(k, prefix) {
			merged[k] = val
		}
	}
	for k, w := range v.writes {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if w == nil {
			delete(merged, k)
		} else {
			merged[k] = *w
		}
	}
	out := make([]KV, 0, len(merged))
	for k, val := range merged {
		out = append(out, KV{Key: k, Value: val})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
