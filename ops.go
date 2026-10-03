package bitmap

import "sort"

// view 是位图容器索引的只读视图（在持锁期间有效）。
type view struct {
	keys []uint16
	ctrs []container
}

// lockViews 按指针序对输入位图加读锁（去重），避免死锁。
// 返回的视图仅在 unlock 调用前有效。
func lockViews(bs []*Bitmap) ([]view, func()) {
	uniq := make(map[*Bitmap]struct{}, len(bs))
	ptrs := make([]*Bitmap, 0, len(bs))
	for _, b := range bs {
		if b == nil {
			continue
		}
		if _, ok := uniq[b]; !ok {
			uniq[b] = struct{}{}
			ptrs = append(ptrs, b)
		}
	}
	sort.Slice(ptrs, func(i, j int) bool { return uintptrOf(ptrs[i]) < uintptrOf(ptrs[j]) })
	for _, b := range ptrs {
		b.mu.RLock()
	}
	views := make([]view, len(ptrs))
	for i, b := range ptrs {
		views[i] = view{keys: b.keys, ctrs: b.ctrs}
	}
	unlock := func() {
		for i := len(ptrs) - 1; i >= 0; i-- {
			ptrs[i].mu.RUnlock()
		}
	}
	return views, unlock
}

// andViews / orViews / xorViews 在键对齐的容器列表上做归并。
func andViews(a, b view) view {
	var keys []uint16
	var ctrs []container
	i, j := 0, 0
	for i < len(a.keys) && j < len(b.keys) {
		switch {
		case a.keys[i] < b.keys[j]:
			i++
		case a.keys[i] > b.keys[j]:
			j++
		default:
			if c := andContainers(a.ctrs[i], b.ctrs[j]); c.cardinality() > 0 {
				keys = append(keys, a.keys[i])
				ctrs = append(ctrs, c)
			}
			i++
			j++
		}
	}
	return view{keys, ctrs}
}

func orViews(a, b view) view {
	var keys []uint16
	var ctrs []container
	i, j := 0, 0
	for i < len(a.keys) || j < len(b.keys) {
		switch {
		case j >= len(b.keys) || (i < len(a.keys) && a.keys[i] < b.keys[j]):
			keys = append(keys, a.keys[i])
			ctrs = append(ctrs, a.ctrs[i].clone())
			i++
		case i >= len(a.keys) || b.keys[j] < a.keys[i]:
			keys = append(keys, b.keys[j])
			ctrs = append(ctrs, b.ctrs[j].clone())
			j++
		default:
			keys = append(keys, a.keys[i])
			ctrs = append(ctrs, orContainers(a.ctrs[i], b.ctrs[j]))
			i++
			j++
		}
	}
	return view{keys, ctrs}
}

func xorViews(a, b view) view {
	var keys []uint16
	var ctrs []container
	i, j := 0, 0
	for i < len(a.keys) || j < len(b.keys) {
		switch {
		case j >= len(b.keys) || (i < len(a.keys) && a.keys[i] < b.keys[j]):
			keys = append(keys, a.keys[i])
			ctrs = append(ctrs, a.ctrs[i].clone())
			i++
		case i >= len(a.keys) || b.keys[j] < a.keys[i]:
			keys = append(keys, b.keys[j])
			ctrs = append(ctrs, b.ctrs[j].clone())
			j++
		default:
			if c := xorContainers(a.ctrs[i], b.ctrs[j]); c.cardinality() > 0 {
				keys = append(keys, a.keys[i])
				ctrs = append(ctrs, c)
			}
			i++
			j++
		}
	}
	return view{keys, ctrs}
}

func bitmapFromView(v view) *Bitmap {
	b := &Bitmap{keys: v.keys, ctrs: v.ctrs, gen: 1}
	b.gens = make([]uint64, len(v.keys))
	for i := range b.gens {
		b.gens[i] = 1
	}
	return b
}

// combine 对全部输入位图依次折叠二元视图运算。
func combine(bs []*Bitmap, op func(a, b view) view) *Bitmap {
	views, unlock := lockViews(bs)
	defer unlock()
	if len(views) == 0 {
		return New()
	}
	acc := views[0]
	for _, v := range views[1:] {
		acc = op(acc, v)
	}
	// 复制键切片，避免结果与输入共享底层数组。
	keys := make([]uint16, len(acc.keys))
	copy(keys, acc.keys)
	return bitmapFromView(view{keys: keys, ctrs: acc.ctrs})
}

// And 计算所有输入位图的交集（批量）。
func And(bs ...*Bitmap) *Bitmap { return combine(bs, andViews) }

// Or 计算所有输入位图的并集（批量）。
func Or(bs ...*Bitmap) *Bitmap { return combine(bs, orViews) }

// Xor 计算所有输入位图的对称差（批量）。
func Xor(bs ...*Bitmap) *Bitmap { return combine(bs, xorViews) }

// And 返回 b 与 other 的交集（新位图）。
func (b *Bitmap) And(other *Bitmap) *Bitmap { return And(b, other) }

// Or 返回 b 与 other 的并集（新位图）。
func (b *Bitmap) Or(other *Bitmap) *Bitmap { return Or(b, other) }

// Xor 返回 b 与 other 的对称差（新位图）。
func (b *Bitmap) Xor(other *Bitmap) *Bitmap { return Xor(b, other) }
