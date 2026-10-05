package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Project 描述一个候选项目。
// Value 为项目自身的固定收益（可为负），协同收益不允许摊入该字段，
// 必须全部通过 Instance.Synergies 以成对形式表达。
type Project struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Cost     int64    `json:"cost"`
	Value    int64    `json:"value"`
	Requires []string `json:"requires"` // 前置项目：选中本项目必须同时选中
	Excludes []string `json:"excludes"` // 互斥项目：与本项目不可同时选中
}

// Synergy 为一对项目同时被选中时的额外收益，允许为负（负协同）。
type Synergy struct {
	A     string `json:"a"`
	B     string `json:"b"`
	Value int64  `json:"value"`
}

// Instance 为求解器的完整输入。
type Instance struct {
	Budget    int64     `json:"budget"`
	Projects  []Project `json:"projects"`
	Synergies []Synergy `json:"synergies"`
}

// LoadInstance 从 JSON 文件读取输入。
func LoadInstance(path string) (Instance, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Instance{}, err
	}
	return ParseInstance(data)
}

// ParseInstance 从 JSON 字节流解析输入。
func ParseInstance(data []byte) (Instance, error) {
	var inst Instance
	if err := json.Unmarshal(data, &inst); err != nil {
		return Instance{}, fmt.Errorf("invalid JSON: %v", err)
	}
	return inst, nil
}
