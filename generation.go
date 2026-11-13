package netsec

import (
	"sync"
	"sync/atomic"
)

// LayerAdapter is one validated, immutable LoRA factor pair in a generation.
// Tensor accessors return copies so callers cannot mutate admitted weights.
type LayerAdapter struct {
	layerID string
	rank    int
	scale   Rational
	dtype   DType
	aDigest Digest
	bDigest Digest
	aShape  TensorShape
	bShape  TensorShape

	mu sync.RWMutex
	a  []byte
	b  []byte
}

// LayerID returns the stable base-layer ID.
func (l *LayerAdapter) LayerID() string {
	return l.layerID
}

// Rank returns the adapter rank for this layer.
func (l *LayerAdapter) Rank() int {
	return l.rank
}

// Scale returns the exact rational scale.
func (l *LayerAdapter) Scale() Rational {
	return l.scale
}

// DType returns the signed factor dtype.
func (l *LayerAdapter) DType() DType {
	return l.dtype
}

// ADigest returns the signed digest of A.
func (l *LayerAdapter) ADigest() Digest {
	return l.aDigest
}

// BDigest returns the signed digest of B.
func (l *LayerAdapter) BDigest() Digest {
	return l.bDigest
}

// AShape returns A's [rank,inputDim] shape.
func (l *LayerAdapter) AShape() TensorShape {
	return l.aShape
}

// BShape returns B's [outputDim,rank] shape.
func (l *LayerAdapter) BShape() TensorShape {
	return l.bShape
}

// A returns a copy of A's raw tensor bytes.
func (l *LayerAdapter) A() []byte {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]byte(nil), l.a...)
}

// B returns a copy of B's raw tensor bytes.
func (l *LayerAdapter) B() []byte {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]byte(nil), l.b...)
}

// Tensors returns copies of both factor tensors.
func (l *LayerAdapter) Tensors() (a, b []byte) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]byte(nil), l.a...), append([]byte(nil), l.b...)
}

func (l *LayerAdapter) zero() {
	l.mu.Lock()
	zeroBytes(l.a)
	zeroBytes(l.b)
	l.mu.Unlock()
}

// Generation is a completely validated adapter model generation. A generation
// is published atomically and remains alive while any request Lease holds it.
type Generation struct {
	id              uint64
	adapterDigest   Digest
	baseModelDigest Digest
	version         string
	layers          map[string]*LayerAdapter
	ordered         []*LayerAdapter

	refs     atomic.Int64
	zeroOnce sync.Once
	zeroed   atomic.Bool
}

// ID returns the monotonically increasing local generation ID.
func (g *Generation) ID() uint64 {
	return g.id
}

// AdapterDigest returns the admitted adapter digest.
func (g *Generation) AdapterDigest() Digest {
	return g.adapterDigest
}

// BaseModelDigest returns the exact base model digest bound at admission.
func (g *Generation) BaseModelDigest() Digest {
	return g.baseModelDigest
}

// Version returns the signed adapter version.
func (g *Generation) Version() string {
	return g.version
}

// LayerCount returns the number of mounted target layers.
func (g *Generation) LayerCount() int {
	return len(g.ordered)
}

// Layer returns the adapter binding for a stable base-layer ID.
func (g *Generation) Layer(layerID string) (*LayerAdapter, bool) {
	layer, ok := g.layers[layerID]
	return layer, ok
}

// Layers returns all bindings in manifest order.
func (g *Generation) Layers() []*LayerAdapter {
	return append([]*LayerAdapter(nil), g.ordered...)
}

// Zeroized reports whether the increment tensors were cleared after the last
// reference was released.
func (g *Generation) Zeroized() bool {
	return g.zeroed.Load()
}

func (g *Generation) retain() bool {
	for {
		current := g.refs.Load()
		if current == 0 {
			return false
		}
		if g.refs.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func (g *Generation) release() {
	for {
		current := g.refs.Load()
		if current <= 0 {
			return
		}
		if g.refs.CompareAndSwap(current, current-1) {
			if current == 1 {
				g.zeroTensors()
			}
			return
		}
	}
}

func (g *Generation) zeroTensors() {
	g.zeroOnce.Do(func() {
		for _, layer := range g.ordered {
			layer.zero()
		}
		g.zeroed.Store(true)
	})
}

// Lease pins a generation for one in-flight request. Release must be called
// exactly once when the request is done; repeated calls are safe no-ops.
type Lease struct {
	generation *Generation
	released   atomic.Bool
}

// Generation returns the pinned generation. Callers must not use it after
// Release.
func (l *Lease) Generation() *Generation {
	return l.generation
}

// Release drops this request's reference. The final reference clears all
// adapter increment tensors.
func (l *Lease) Release() {
	if l == nil || l.generation == nil {
		return
	}
	if l.released.CompareAndSwap(false, true) {
		l.generation.release()
	}
}
