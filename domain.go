package netsec

import "fmt"

// Domain 是调用方显式给出的有限调用与参数域：一组系统调用号，加上每个参数位
// 可取的有限值集合。组合包只在该有限域上保证“追加只能收窄”，从而让收窄性质
// 可以被穷举验证，而不依赖对全部 64 位参数空间的假设。
type Domain struct {
	syscalls []uint
	argVals  [maxModeledArgs][]uint64
}

// NewDomain 校验并构造有限域。syscalls 与每个参数位的取值列表会被复制并排序去重。
// 调用方通常只列出沙箱关心的系统调用与有限参数取值（如文件描述符、标志位）。
func NewDomain(syscalls []uint, argValues [maxModeledArgs][]uint64) (*Domain, error) {
	d := &Domain{
		syscalls: append([]uint(nil), syscalls...),
	}
	for i := range maxModeledArgs {
		d.argVals[i] = append([]uint64(nil), argValues[i]...)
	}
	sortUniqueUint(d.syscalls)
	for i := range maxModeledArgs {
		sortUniqueUint64(d.argVals[i])
	}
	if len(d.syscalls) == 0 {
		return nil, fmt.Errorf("netsec: domain must contain at least one syscall")
	}
	return d, nil
}

// Syscalls 返回域内系统调用号的排序副本。
func (d *Domain) Syscalls() []uint { return append([]uint(nil), d.syscalls...) }

// ArgValues 返回第 pos 个参数位可取数值的排序副本。
func (d *Domain) ArgValues(pos int) []uint64 {
	if pos < 0 || pos >= maxModeledArgs {
		return nil
	}
	return append([]uint64(nil), d.argVals[pos]...)
}

// Size 返回域内请求键总数（系统调用数与各参数位取值数的笛卡尔积）。
func (d *Domain) Size() int {
	size := len(d.syscalls)
	for i := range maxModeledArgs {
		size *= len(d.argVals[i])
	}
	return size
}

// Enumerate 按确定顺序返回域内全部具体请求键：每个系统调用与参数位取值做
// 笛卡尔积，键的 ArgMask 标记两个参数位均参与匹配。
func (d *Domain) Enumerate() []Key {
	keys := make([]Key, 0, d.Size())
	for _, syscall := range d.syscalls {
		d.enumerateArgs(syscall, 0, Key{Syscall: syscall}, &keys)
	}
	return keys
}

func (d *Domain) enumerateArgs(syscall uint, pos int, prefix Key, out *[]Key) {
	if pos == maxModeledArgs {
		prefix.ArgMask = 1<<maxModeledArgs - 1
		*out = append(*out, prefix)
		return
	}
	values := d.argVals[pos]
	if len(values) == 0 {
		d.enumerateArgs(syscall, pos+1, prefix, out)
		return
	}
	for _, value := range values {
		next := prefix
		next.Args[pos] = value
		d.enumerateArgs(syscall, pos+1, next, out)
	}
}

func sortUniqueUint(values []uint) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
	kept := values[:0]
	for _, value := range values {
		if len(kept) > 0 && kept[len(kept)-1] == value {
			continue
		}
		kept = append(kept, value)
	}
}

func sortUniqueUint64(values []uint64) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
	kept := values[:0]
	for _, value := range values {
		if len(kept) > 0 && kept[len(kept)-1] == value {
			continue
		}
		kept = append(kept, value)
	}
}
