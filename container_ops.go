package bitmap

// intersectContainers returns a normalized container for a AND b,
// or nil when the result is empty.
func intersectContainers(a, b *container, threshold int) *container {
	var out container
	switch {
	case a.kind == kindArray && b.kind == kindArray:
		var arr []uint16
		i, j := 0, 0
		for i < len(a.array) && j < len(b.array) {
			switch {
			case a.array[i] == b.array[j]:
				arr = append(arr, a.array[i])
				i++
				j++
			case a.array[i] < b.array[j]:
				i++
			default:
				j++
			}
		}
		out = newArrayContainer(arr)
	case a.kind == kindBitmap && b.kind == kindBitmap:
		words := make([]uint64, bitmapWords)
		for i := range words {
			words[i] = a.words[i] & b.words[i]
		}
		out = newBitmapContainer(words)
	default:
		arr, bm := a, b
		if arr.kind == kindBitmap {
			arr, bm = b, a
		}
		var res []uint16
		for _, v := range arr.array {
			if bm.words[v>>6]&(1<<(v&63)) != 0 {
				res = append(res, v)
			}
		}
		out = newArrayContainer(res)
	}
	if out.card == 0 {
		return nil
	}
	out.normalize(threshold)
	return &out
}

// unionContainers returns a normalized container for a OR b.
func unionContainers(a, b *container, threshold int) *container {
	var out container
	switch {
	case a.kind == kindArray && b.kind == kindArray:
		arr := make([]uint16, 0, len(a.array)+len(b.array))
		i, j := 0, 0
		for i < len(a.array) && j < len(b.array) {
			switch {
			case a.array[i] == b.array[j]:
				arr = append(arr, a.array[i])
				i++
				j++
			case a.array[i] < b.array[j]:
				arr = append(arr, a.array[i])
				i++
			default:
				arr = append(arr, b.array[j])
				j++
			}
		}
		arr = append(arr, a.array[i:]...)
		arr = append(arr, b.array[j:]...)
		out = newArrayContainer(arr)
	case a.kind == kindBitmap && b.kind == kindBitmap:
		words := make([]uint64, bitmapWords)
		for i := range words {
			words[i] = a.words[i] | b.words[i]
		}
		out = newBitmapContainer(words)
	default:
		arr, bm := a, b
		if arr.kind == kindBitmap {
			arr, bm = b, a
		}
		words := make([]uint64, bitmapWords)
		copy(words, bm.words)
		for _, v := range arr.array {
			words[v>>6] |= 1 << (v & 63)
		}
		out = newBitmapContainer(words)
	}
	out.normalize(threshold)
	return &out
}

// andNotContainers returns a normalized container for a \ b,
// or nil when the result is empty.
func andNotContainers(a, b *container, threshold int) *container {
	var out container
	switch {
	case a.kind == kindArray && b.kind == kindArray:
		var arr []uint16
		i, j := 0, 0
		for i < len(a.array) {
			for j < len(b.array) && b.array[j] < a.array[i] {
				j++
			}
			if j < len(b.array) && b.array[j] == a.array[i] {
				i++
				continue
			}
			arr = append(arr, a.array[i])
			i++
		}
		out = newArrayContainer(arr)
	case a.kind == kindBitmap && b.kind == kindBitmap:
		words := make([]uint64, bitmapWords)
		for i := range words {
			words[i] = a.words[i] &^ b.words[i]
		}
		out = newBitmapContainer(words)
	case a.kind == kindArray: // array \ bitmap
		var arr []uint16
		for _, v := range a.array {
			if b.words[v>>6]&(1<<(v&63)) == 0 {
				arr = append(arr, v)
			}
		}
		out = newArrayContainer(arr)
	default: // bitmap \ array
		words := make([]uint64, bitmapWords)
		copy(words, a.words)
		for _, v := range b.array {
			words[v>>6] &^= 1 << (v & 63)
		}
		out = newBitmapContainer(words)
	}
	if out.card == 0 {
		return nil
	}
	out.normalize(threshold)
	return &out
}
