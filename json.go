package ra

import (
	"encoding/json"
	"fmt"
	"math"
)

// Input 是 CLI 的输入文件格式：有限表数据 + 一个计划。
type Input struct {
	Tables map[string][]Row
	Plan   Node
}

// ParseInput 解析 {"tables": {...}, "plan": {...}} 格式的 JSON。
func ParseInput(data []byte) (*Input, error) {
	var raw struct {
		Tables map[string][]map[string]any `json:"tables"`
		Plan   map[string]any              `json:"plan"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %v", err)
	}
	in := &Input{Tables: map[string][]Row{}}
	for name, rows := range raw.Tables {
		for _, r := range rows {
			in.Tables[name] = append(in.Tables[name], normalizeRow(r))
		}
	}
	if raw.Plan == nil {
		return nil, fmt.Errorf("缺少 plan 字段")
	}
	p, err := DecodeNode(raw.Plan, in.Tables)
	if err != nil {
		return nil, err
	}
	in.Plan = p
	return in, nil
}

// normalizeRow 把 JSON 数（float64）规范为 int64（可整除时），null 保持 nil。
func normalizeRow(m map[string]any) Row {
	row := Row{}
	for k, v := range m {
		row[k] = normalizeValue(v)
	}
	return row
}

func normalizeValue(v any) any {
	switch t := v.(type) {
	case float64:
		if t == math.Trunc(t) && !math.IsInf(t, 0) {
			return int64(t)
		}
		return t
	case map[string]any, []any:
		return fmt.Sprintf("%v", t)
	default:
		return v
	}
}

// DecodeNode 把计划 JSON 解码为 Node。tables 用于解析 scan。
func DecodeNode(m map[string]any, tables map[string][]Row) (Node, error) {
	op, _ := m["op"].(string)
	switch op {
	case "scan":
		name, _ := m["table"].(string)
		rows, ok := tables[name]
		if !ok {
			return nil, fmt.Errorf("scan 引用了未知表 %q", name)
		}
		var cols []string
		if raw, ok := m["cols"].([]any); ok {
			for _, c := range raw {
				s, _ := c.(string)
				cols = append(cols, s)
			}
		}
		return &Scan{Name: name, Rows: rows, Cols: cols}, nil
	case "project":
		input, err := DecodeNode(child(m, "input"), tables)
		if err != nil {
			return nil, err
		}
		rawExprs, _ := m["exprs"].([]any)
		var exprs []NamedExpr
		for _, re := range rawExprs {
			em, _ := re.(map[string]any)
			ex, err := decodeExpr(child(em, "expr"), tables)
			if err != nil {
				return nil, err
			}
			as, _ := em["as"].(string)
			exprs = append(exprs, NamedExpr{Expr: ex, As: as})
		}
		return &Project{Input: input, Exprs: exprs}, nil
	case "filter":
		input, err := DecodeNode(child(m, "input"), tables)
		if err != nil {
			return nil, err
		}
		pred, err := decodeExpr(child(m, "pred"), tables)
		if err != nil {
			return nil, err
		}
		return &Filter{Input: input, Pred: pred}, nil
	case "join":
		left, err := DecodeNode(child(m, "left"), tables)
		if err != nil {
			return nil, err
		}
		right, err := DecodeNode(child(m, "right"), tables)
		if err != nil {
			return nil, err
		}
		pred, err := decodeExpr(child(m, "pred"), tables)
		if err != nil {
			return nil, err
		}
		kind := InnerJoin
		if how, _ := m["how"].(string); how == "left" {
			kind = LeftJoin
		} else if how != "" && how != "inner" {
			return nil, fmt.Errorf("未知连接类型 %q", how)
		}
		return &Join{Kind: kind, Left: left, Right: right, Pred: pred}, nil
	case "semijoin":
		left, err := DecodeNode(child(m, "left"), tables)
		if err != nil {
			return nil, err
		}
		right, err := DecodeNode(child(m, "right"), tables)
		if err != nil {
			return nil, err
		}
		pred, err := decodeExpr(child(m, "pred"), tables)
		if err != nil {
			return nil, err
		}
		return &SemiJoin{Left: left, Right: right, Pred: pred}, nil
	case "groupby":
		input, err := DecodeNode(child(m, "input"), tables)
		if err != nil {
			return nil, err
		}
		var keys []string
		for _, k := range toList(m["keys"]) {
			s, _ := k.(string)
			keys = append(keys, s)
		}
		var aggs []AggSpec
		for _, ra := range toList(m["aggs"]) {
			am, _ := ra.(map[string]any)
			fn, _ := am["func"].(string)
			col, _ := am["col"].(string)
			as, _ := am["as"].(string)
			aggs = append(aggs, AggSpec{Func: fn, Col: col, As: as})
		}
		return &GroupBy{Input: input, Keys: keys, Aggs: aggs}, nil
	}
	return nil, fmt.Errorf("未知计划算子 %q", op)
}

// DecodeExpr 把表达式 JSON 解码为 Expr。tables 参数用于子查询中的 scan。
func DecodeExpr(m map[string]any) (Expr, error) {
	return decodeExpr(m, nil)
}

func decodeExpr(m map[string]any, tables map[string][]Row) (Expr, error) {
	kind, _ := m["kind"].(string)
	switch kind {
	case "lit":
		return &Lit{V: normalizeValue(m["value"])}, nil
	case "col":
		name, _ := m["name"].(string)
		return &Col{Name: name}, nil
	case "outer":
		name, _ := m["name"].(string)
		return &Outer{Name: name}, nil
	case "and", "or", "coalesce":
		var args []Expr
		for _, ra := range toList(m["args"]) {
			e, err := decodeExpr(asMap(ra), tables)
			if err != nil {
				return nil, err
			}
			args = append(args, e)
		}
		switch kind {
		case "and":
			return &And{Args: args}, nil
		case "or":
			return &Or{Args: args}, nil
		default:
			return &Coalesce{Args: args}, nil
		}
	case "not":
		e, err := decodeExpr(child(m, "arg"), tables)
		if err != nil {
			return nil, err
		}
		return &Not{E: e}, nil
	case "cmp":
		l, err := decodeExpr(child(m, "l"), tables)
		if err != nil {
			return nil, err
		}
		r, err := decodeExpr(child(m, "r"), tables)
		if err != nil {
			return nil, err
		}
		op, _ := m["op"].(string)
		return &Cmp{Op: op, L: l, R: r}, nil
	case "isnull":
		e, err := decodeExpr(child(m, "arg"), tables)
		if err != nil {
			return nil, err
		}
		return &IsNull{E: e}, nil
	case "exists":
		sub, err := DecodeNode(child(m, "plan"), tables)
		if err != nil {
			return nil, err
		}
		return &Exists{Sub: sub}, nil
	case "count":
		sub, err := DecodeNode(child(m, "plan"), tables)
		if err != nil {
			return nil, err
		}
		col, _ := m["col"].(string)
		if col == "" {
			col = "*"
		}
		return &CountSub{Col: col, Sub: sub}, nil
	}
	return nil, fmt.Errorf("未知表达式 kind %q", kind)
}

func child(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	sub, _ := m[key].(map[string]any)
	return sub
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func toList(v any) []any {
	l, _ := v.([]any)
	return l
}
