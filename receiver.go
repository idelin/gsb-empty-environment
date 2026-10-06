package main

import (
	"encoding/json"
	"io/ioutil"
	"os"
)

// Receiver 是幂等接收模型：按结果 ID 去重，
// 同一 ID 重复投递只确认、不重复计数；
// 同时校验结果区间连续性，发现空洞/重叠会计入 GapErrors。
type Receiver struct {
	store      *receiverStore
	applied    map[string]bool
	Total      int64
	Deliveries int64 // 总投递次数
	Duplicates int64 // 其中重复投递次数（已被去重，未计入 Total）
	GapErrors  int64
	expectFrom int64
}

type receiverState struct {
	AppliedIDs []string `json:"applied_ids"`
	Total      int64    `json:"total"`
	ExpectFrom int64    `json:"expect_from"`
}

type receiverStore struct{ path string }

func NewReceiver(store *receiverStore) *Receiver {
	r := &Receiver{store: store, applied: make(map[string]bool)}
	b, err := ioutil.ReadFile(store.path)
	if err == nil {
		var s receiverState
		if json.Unmarshal(b, &s) == nil {
			for _, id := range s.AppliedIDs {
				r.applied[id] = true
			}
			r.Total = s.Total
			r.expectFrom = s.ExpectFrom
		}
	}
	return r
}

// Apply 应用结果并返回确认。重复 ID 直接确认、不计数。
func (r *Receiver) Apply(res Result) error {
	r.Deliveries++
	if r.applied[res.ID] {
		r.Duplicates++
		return nil
	}
	if res.FromOffset != r.expectFrom {
		r.GapErrors++
	}
	r.applied[res.ID] = true
	r.Total += res.Delta
	r.expectFrom = res.ToOffset
	return r.persist()
}

func (r *Receiver) AppliedCount() int { return len(r.applied) }

func (r *Receiver) persist() error {
	s := receiverState{Total: r.Total, ExpectFrom: r.expectFrom}
	for id := range r.applied {
		s.AppliedIDs = append(s.AppliedIDs, id)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.store.path + ".tmp"
	if err := ioutil.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, r.store.path)
}
