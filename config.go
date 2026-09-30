package main

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

type fileConfig struct {
	Rules []jsonRule `json:"rules"`
	Sim   *jsonSim   `json:"sim"`
}

type jsonRule struct {
	Name   string          `json:"name"`
	Prefix string          `json:"prefix"`
	Proto  json.RawMessage `json:"proto"` // "tcp" | ["tcp","udp"] | "any"
	Ports  string          `json:"ports"` // "any" | "80" | "8000-8080"
	Action string          `json:"action"`
}

type jsonSim struct {
	ServiceTicks  int `json:"serviceTicks"`
	LinkDelay     int `json:"linkDelay"`
	BytesPerTick  int `json:"bytesPerTick"`
	QueueCapacity int `json:"queueCapacity"`
}

func parsePorts(s string) (uint16, uint16, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "any") {
		return 0, 65535, nil
	}
	parse := func(v string) (uint16, error) {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 0 || n > 65535 {
			return 0, fmt.Errorf("非法端口 %q", v)
		}
		return uint16(n), nil
	}
	if lo, hi, ok := strings.Cut(s, "-"); ok {
		l, err := parse(lo)
		if err != nil {
			return 0, 0, err
		}
		h, err := parse(hi)
		if err != nil {
			return 0, 0, err
		}
		if l > h {
			return 0, 0, fmt.Errorf("端口区间 %q 下界大于上界", s)
		}
		return l, h, nil
	}
	p, err := parse(s)
	return p, p, err
}

func parseProtoField(raw json.RawMessage) (Protocol, error) {
	if len(raw) == 0 {
		return ProtoAny, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return parseProtocolName(single)
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return 0, fmt.Errorf("proto 字段须为字符串或字符串数组")
	}
	var out Protocol
	for _, s := range list {
		p, err := parseProtocolName(s)
		if err != nil {
			return 0, err
		}
		out |= p
	}
	if out == 0 {
		return 0, fmt.Errorf("proto 列表为空")
	}
	return out, nil
}

// LoadConfig 从 JSON 文件加载规则与仿真参数。
// 允许同时含 IPv4 与 IPv6 规则，但二者在匹配与分析中严格隔离。
func LoadConfig(path string) ([]Rule, SimConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, SimConfig{}, err
	}
	var fc fileConfig
	if err := json.Unmarshal(data, &fc); err != nil {
		return nil, SimConfig{}, fmt.Errorf("解析 JSON 失败: %w", err)
	}
	if len(fc.Rules) == 0 {
		return nil, SimConfig{}, fmt.Errorf("规则列表为空")
	}
	rules := make([]Rule, 0, len(fc.Rules))
	for i, jr := range fc.Rules {
		pfx, err := netip.ParsePrefix(jr.Prefix)
		if err != nil {
			return nil, SimConfig{}, fmt.Errorf("规则 %d：前缀 %q 非法: %w", i, jr.Prefix, err)
		}
		protos, err := parseProtoField(jr.Proto)
		if err != nil {
			return nil, SimConfig{}, fmt.Errorf("规则 %d：%w", i, err)
		}
		lo, hi, err := parsePorts(jr.Ports)
		if err != nil {
			return nil, SimConfig{}, fmt.Errorf("规则 %d：%w", i, err)
		}
		var action Action
		switch strings.ToLower(jr.Action) {
		case "allow":
			action = ActionAllow
		case "deny":
			action = ActionDeny
		default:
			return nil, SimConfig{}, fmt.Errorf("规则 %d：动作 %q 非法（allow/deny）", i, jr.Action)
		}
		name := jr.Name
		if name == "" {
			name = fmt.Sprintf("rule-%d", i)
		}
		rules = append(rules, Rule{
			Name:    name,
			Prefix:  pfx.Masked(),
			Protos:  protos,
			PortMin: lo,
			PortMax: hi,
			Action:  action,
		})
	}
	if err := ValidateRules(rules); err != nil {
		return nil, SimConfig{}, err
	}
	cfg := SimConfig{ServiceTicks: 1, LinkDelay: 2, BytesPerTick: 1500, QueueCapacity: 0}
	if fc.Sim != nil {
		cfg.ServiceTicks = fc.Sim.ServiceTicks
		cfg.LinkDelay = fc.Sim.LinkDelay
		cfg.BytesPerTick = fc.Sim.BytesPerTick
		cfg.QueueCapacity = fc.Sim.QueueCapacity
	}
	return rules, cfg, nil
}
