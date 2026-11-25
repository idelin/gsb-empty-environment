package typeverifier

import "fmt"

// valTag 区分操作数栈/局部槽中引用值的抽象类别。
type valTag int

const (
	valTop valTag = iota
	valInit
	valUninit
)

// value 是一个引用抽象值。
type value struct {
	tag   valTag
	class string
	newPC int
}

func topValue() value { return value{tag: valTop} }

func initValue(class string) value {
	return value{tag: valInit, class: class, newPC: -1}
}

func uninitValue(class string, newPC int) value {
	return value{tag: valUninit, class: class, newPC: newPC}
}

func (v value) isTop() bool    { return v.tag == valTop }
func (v value) isInit() bool   { return v.tag == valInit }
func (v value) isUninit() bool { return v.tag == valUninit }

func (v value) String() string {
	switch v.tag {
	case valTop:
		return "top"
	case valInit:
		return "init(" + v.class + ")"
	default:
		return fmt.Sprintf("uninit(%s@%d)", v.class, v.newPC)
	}
}

// frame 是某条指令入口处的抽象栈帧：局部槽与操作数栈。
type frame struct {
	locals []value
	stack  []value
}

func newFrame(maxLocals int) *frame {
	f := &frame{
		locals: make([]value, maxLocals),
		stack:  make([]value, 0),
	}
	for i := range f.locals {
		f.locals[i] = topValue()
	}
	return f
}

func (f *frame) clone() *frame {
	return &frame{
		locals: append([]value(nil), f.locals...),
		stack:  append([]value(nil), f.stack...),
	}
}

// hasUninit 报告帧中（局部槽或操作数栈）是否携带未初始化引用。
func (f *frame) hasUninit() bool {
	for _, v := range f.locals {
		if v.isUninit() {
			return true
		}
	}
	for _, v := range f.stack {
		if v.isUninit() {
			return true
		}
	}
	return false
}
