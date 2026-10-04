package policydiff

import (
	"encoding/json"
	"fmt"
)

// UnmarshalJSON 从 JSON 解码规则表达式。支持的形式：
//
//	{"and": [expr, ...]}
//	{"or": [expr, ...]}
//	{"not": expr}
//	{"intRange": {"attr": "subject.level", "lo": 5, "hi": 10}}
//	{"labelIn": {"attr": "resource.label", "set": ["public", "internal"]}}
//	{"bool": {"attr": "subject.mfa", "value": true}}
func (e *Expr) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw) != 1 {
		return fmt.Errorf("表达式节点须且仅须包含一个操作符，实际 %d 个", len(raw))
	}
	for key, payload := range raw {
		switch key {
		case "and", "or":
			var kids []*Expr
			if err := json.Unmarshal(payload, &kids); err != nil {
				return err
			}
			if len(kids) == 0 {
				return fmt.Errorf("%s 至少需要一个子表达式", key)
			}
			if key == "and" {
				*e = *And(kids...)
			} else {
				*e = *Or(kids...)
			}
		case "not":
			var kid Expr
			if err := json.Unmarshal(payload, &kid); err != nil {
				return err
			}
			*e = *Not(&kid)
		case "intRange":
			var v struct {
				Attr string `json:"attr"`
				Lo   int    `json:"lo"`
				Hi   int    `json:"hi"`
			}
			if err := json.Unmarshal(payload, &v); err != nil {
				return err
			}
			if v.Lo > v.Hi {
				return fmt.Errorf("intRange 下界 %d 大于上界 %d", v.Lo, v.Hi)
			}
			*e = *IntRange(v.Attr, v.Lo, v.Hi)
		case "labelIn":
			var v struct {
				Attr string   `json:"attr"`
				Set  []string `json:"set"`
			}
			if err := json.Unmarshal(payload, &v); err != nil {
				return err
			}
			*e = *LabelIn(v.Attr, v.Set...)
		case "bool":
			var v struct {
				Attr  string `json:"attr"`
				Value bool   `json:"value"`
			}
			if err := json.Unmarshal(payload, &v); err != nil {
				return err
			}
			*e = *BoolCond(v.Attr, v.Value)
		default:
			return fmt.Errorf("未知操作符 %q", key)
		}
	}
	return nil
}

// ParseRule 从 JSON 字节解码一条规则。
func ParseRule(data []byte) (*Expr, error) {
	var e Expr
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("解析规则失败：%w", err)
	}
	return &e, nil
}

// ParseSchema 从 JSON 字节解码属性域模式：
//
//	{"attributes": {
//	  "subject.level":  {"type": "int", "min": 0, "max": 10},
//	  "resource.label": {"type": "label", "values": ["public", "internal"]},
//	  "subject.mfa":    {"type": "bool"}
//	}}
func ParseSchema(data []byte) (Schema, error) {
	var doc struct {
		Attributes map[string]struct {
			Type   string   `json:"type"`
			Min    int      `json:"min"`
			Max    int      `json:"max"`
			Values []string `json:"values"`
		} `json:"attributes"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("解析模式失败：%w", err)
	}
	s := Schema{}
	for name, d := range doc.Attributes {
		s[name] = Domain{Type: d.Type, Min: d.Min, Max: d.Max, Values: d.Values}
	}
	return s, nil
}
