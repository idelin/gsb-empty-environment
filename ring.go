package taskpkg

// ringBuffer is a fixed-capacity FIFO used on the real-time path. Reaching
// capacity only advances an index; it never copies the whole history.
type ringBuffer struct {
	data  []float64
	head  int // next write position
	count int
}

func newRingBuffer(capacity int) ringBuffer {
	if capacity < 1 {
		capacity = 1
	}
	return ringBuffer{data: make([]float64, capacity)}
}

func (r *ringBuffer) append(value float64) {
	r.data[r.head] = value
	r.head = (r.head + 1) % len(r.data)
	if r.count < len(r.data) {
		r.count++
	}
}

// fromEnd returns the value at offset where zero is the newest sample.
func (r *ringBuffer) fromEnd(offset int) float64 {
	if offset < 0 || offset >= r.count {
		return 0
	}
	index := r.head - 1 - offset
	for index < 0 {
		index += len(r.data)
	}
	return r.data[index]
}

// tail returns up to n newest values in chronological order.
func (r *ringBuffer) tail(n int) []float64 {
	if n > r.count {
		n = r.count
	}
	if n <= 0 {
		return nil
	}
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = r.fromEnd(n - 1 - i)
	}
	return out
}

// snapshot returns all retained values in chronological order.
func (r *ringBuffer) snapshot() []float64 {
	return r.tail(r.count)
}

func (r *ringBuffer) reset() {
	r.head = 0
	r.count = 0
}
