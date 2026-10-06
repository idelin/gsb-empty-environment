package main

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"os"
)

// Result 是聚合器发布的一条结果。
// ID 在结果生成时由检查点中持久化的序号派生，
// 恢复后重发同一结果时 ID 不变，接收方据此去重。
type Result struct {
	ID         string `json:"id"`
	Seq        int64  `json:"seq"`
	FromOffset int64  `json:"from_offset"` // 覆盖输入区间 (FromOffset, ToOffset]
	ToOffset   int64  `json:"to_offset"`
	Delta      int64  `json:"delta"` // 本结果承载的增量值
}

// Checkpoint 同时持久化两类进度：
//  1. 计算状态：Offset/Sum/Count（累计值）与已发结果的覆盖水位；
//  2. 效果确认：Pending（已生成待确认的结果）与 NextResultSeq（结果 ID 序号）。
type Checkpoint struct {
	InstanceID        string   `json:"instance_id"`
	Offset            int64    `json:"offset"`
	Sum               int64    `json:"sum"`
	Count             int64    `json:"count"`
	LastEmittedOffset int64    `json:"last_emitted_offset"`
	LastEmittedSum    int64    `json:"last_emitted_sum"`
	NextResultSeq     int64    `json:"next_result_seq"`
	Pending           []Result `json:"pending"`
}

var errSimulatedCrash = errors.New("simulated crash during checkpoint persist")

// Store 负责检查点的原子落盘：先写临时文件，再 rename。
// 崩溃只会留下旧检查点或新检查点，不会出现半截文件。
type Store struct {
	path string
	// CrashAfterTempWrite 是故障注入开关：
	// 临时文件写完后、原子重命名前“崩溃”。
	CrashAfterTempWrite bool
}

func NewStore(path string) *Store { return &Store{path: path} }

func (s *Store) Save(cp Checkpoint) error {
	b, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := ioutil.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	if s.CrashAfterTempWrite {
		return errSimulatedCrash
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Load() (Checkpoint, bool, error) {
	b, err := ioutil.ReadFile(s.path)
	if os.IsNotExist(err) {
		return Checkpoint{}, false, nil
	}
	if err != nil {
		return Checkpoint{}, false, err
	}
	var cp Checkpoint
	if err := json.Unmarshal(b, &cp); err != nil {
		return Checkpoint{}, false, err
	}
	return cp, true, nil
}
