package fxsettle

import "math/big"

// Op 来源记录的操作类型。
type Op string

const (
	OpInput   Op = "input"   // 原始录入
	OpConvert Op = "convert" // 乘以汇率（精确，不舍入）
	OpRound   Op = "round"   // 舍入节点
	OpSum     Op = "sum"     // 汇总
	OpCorrect Op = "correct" // 更正（含反向更正）
)

// ProvRec 一条来源记录：经过什么操作、由哪些条目推出、得到的精确值。
type ProvRec struct {
	Op     Op
	Note   string
	Inputs []string // 输入条目 ID
	Value  *big.Rat // 本操作之后的精确值
}

// Entry 一条账目：币种 + 精确金额 + 完整来源链。
type Entry struct {
	ID   string
	Cur  string
	Val  *big.Rat
	Prov []ProvRec
}

// NewEntry 创建原始账目，来源链以 input 记录起始。
func NewEntry(id, cur string, val *big.Rat, note string) *Entry {
	return &Entry{
		ID:  id,
		Cur: cur,
		Val: new(big.Rat).Set(val),
		Prov: []ProvRec{{
			Op:    OpInput,
			Note:  note,
			Value: new(big.Rat).Set(val),
		}},
	}
}

func (e *Entry) record(op Op, note string, inputs []string, val *big.Rat) {
	e.Prov = append(e.Prov, ProvRec{
		Op:     op,
		Note:   note,
		Inputs: inputs,
		Value:  new(big.Rat).Set(val),
	})
}

// Sum 精确汇总多条账目（要求币种一致），来源链记录全部输入。
func Sum(id string, entries ...*Entry) *Entry {
	if len(entries) == 0 {
		panic("fxsettle: Sum 至少需要一条账目")
	}
	cur := entries[0].Cur
	total := new(big.Rat)
	inputs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Cur != cur {
			panic("fxsettle: Sum 币种不一致: " + e.Cur + " vs " + cur)
		}
		total.Add(total, e.Val)
		inputs = append(inputs, e.ID)
	}
	out := &Entry{ID: id, Cur: cur, Val: total}
	out.record(OpSum, "精确汇总", inputs, total)
	return out
}
