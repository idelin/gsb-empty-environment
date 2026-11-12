package netsec

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// MaxWrites bounds the number of block writes in one op sequence so the
// crash-image enumeration stays exhaustive.
const MaxWrites = 8

// OpKind selects the effect of an Op.
type OpKind uint8

const (
	// OpWrite is a fixed-length block write that lands in the volatile
	// write cache and may be reordered with other cached writes.
	OpWrite OpKind = iota
	// OpFlush is a flush barrier: every write cached before it is
	// persisted before any later write may persist.
	OpFlush
)

// Op is one storage operation issued by the host.
type Op struct {
	Kind  OpKind
	Block string
	Data  string
	Force bool
}

// Write queues a cached fixed-length block write.
func Write(block, data string) Op { return Op{Kind: OpWrite, Block: block, Data: data} }

// ForceWrite queues a block write that is persisted at once.
func ForceWrite(block, data string) Op {
	return Op{Kind: OpWrite, Block: block, Data: data, Force: true}
}

// Flush inserts a flush barrier into the op sequence.
func Flush() Op { return Op{Kind: OpFlush} }

// Image is a crash image: block name to durable payload.
type Image map[string]string

// Recovery inspects one crash image and reports a consistency violation,
// e.g. an acknowledged counter lower bound that regressed.
type Recovery func(Image) error

// Violation pairs a failing crash image with every legal persistence
// ordering that could have produced it.
type Violation struct {
	Image  Image
	Orders [][]string
	Err    error
}

// Enumerate simulates the op sequence against an initial durable image,
// enumerates every distinct crash image that respects the persistence
// dependencies (cached writes reorder freely, flush barriers and forced
// writes constrain them), and feeds each image to rec. It returns the
// deduplicated images plus one Violation per image rec rejects.
func Enumerate(initial Image, ops []Op, rec Recovery) ([]Image, []Violation, error) {
	if rec == nil {
		return nil, nil, errors.New("netsec: recovery callback is nil")
	}
	if err := validate(initial, ops); err != nil {
		return nil, nil, err
	}
	infos := enumerate(initial, ops)
	keys := make([]string, 0, len(infos))
	for k := range infos {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	images := make([]Image, 0, len(keys))
	var violations []Violation
	for _, k := range keys {
		info := infos[k]
		images = append(images, info.image)
		if err := rec(cloneImage(info.image)); err != nil {
			violations = append(violations, Violation{
				Image:  info.image,
				Orders: info.orders(),
				Err:    err,
			})
		}
	}
	return images, violations, nil
}

// Permutations returns every permutation of set in lexicographic order.
// Hosts use it to cross-check Violation.Orders against the full
// permutation space of the lowercase block set.
func Permutations(set []string) [][]string {
	blocks := slices.Clone(set)
	sort.Strings(blocks)
	var out [][]string
	used := make([]bool, len(blocks))
	perm := make([]string, 0, len(blocks))
	var rec func()
	rec = func() {
		if len(perm) == len(blocks) {
			out = append(out, slices.Clone(perm))
			return
		}
		for i, b := range blocks {
			if used[i] {
				continue
			}
			used[i] = true
			perm = append(perm, b)
			rec()
			perm = perm[:len(perm)-1]
			used[i] = false
		}
	}
	rec()
	return out
}

func validate(initial Image, ops []Op) error {
	blockLen := -1
	checkBlock := func(block, data, what string) error {
		if !isLowerName(block) {
			return fmt.Errorf("netsec: %s block name %q is not lowercase", what, block)
		}
		if data == "" {
			return fmt.Errorf("netsec: %s has an empty payload", what)
		}
		if blockLen < 0 {
			blockLen = len(data)
		} else if len(data) != blockLen {
			return fmt.Errorf("netsec: %s payload length %d, want fixed length %d", what, len(data), blockLen)
		}
		return nil
	}
	for block, data := range initial {
		if err := checkBlock(block, data, "initial image"); err != nil {
			return err
		}
	}
	writes := 0
	for i, op := range ops {
		switch op.Kind {
		case OpFlush:
			if op.Block != "" || op.Data != "" || op.Force {
				return fmt.Errorf("netsec: op %d: flush barrier carries no payload", i)
			}
		case OpWrite:
			writes++
			if err := checkBlock(op.Block, op.Data, fmt.Sprintf("op %d", i)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("netsec: op %d: unknown kind %d", i, op.Kind)
		}
	}
	if writes > MaxWrites {
		return fmt.Errorf("netsec: %d block writes exceed the limit of %d", writes, MaxWrites)
	}
	return nil
}

func isLowerName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// imageInfo accumulates every way one distinct crash image can arise:
// segOf maps each block written by the op sequence to the segment (the
// span between two flush barriers) of the write that produced the value
// visible in the image.
type imageInfo struct {
	image   Image
	segMaps map[string]map[string]int
}

// orders returns every legal persistence ordering of the image: all
// permutations of the written blocks whose segment numbers are
// non-decreasing, merged across every crash point that yields the image.
func (info *imageInfo) orders() [][]string {
	seen := map[string]bool{}
	var out [][]string
	for _, segOf := range info.segMaps {
		for _, perm := range legalOrders(segOf) {
			key := strings.Join(perm, "\x00")
			if !seen[key] {
				seen[key] = true
				out = append(out, perm)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return slices.Compare(out[i], out[j]) < 0 })
	return out
}

// legalOrders returns the permutations of segOf's blocks whose segment
// numbers never decrease, i.e. orderings a flush-barriered disk allows.
func legalOrders(segOf map[string]int) [][]string {
	blocks := make([]string, 0, len(segOf))
	for b := range segOf {
		blocks = append(blocks, b)
	}
	sort.Strings(blocks)
	var out [][]string
	used := make([]bool, len(blocks))
	perm := make([]string, 0, len(blocks))
	var rec func()
	rec = func() {
		if len(perm) == len(blocks) {
			out = append(out, slices.Clone(perm))
			return
		}
		last := -1
		if len(perm) > 0 {
			last = segOf[perm[len(perm)-1]]
		}
		for i, b := range blocks {
			if used[i] || segOf[b] < last {
				continue
			}
			used[i] = true
			perm = append(perm, b)
			rec()
			perm = perm[:len(perm)-1]
			used[i] = false
		}
	}
	rec()
	return out
}

// enumerate walks every crash point of the op sequence and, at each one,
// applies every subset of the still-cached writes to the persisted state.
func enumerate(initial Image, ops []Op) map[string]*imageInfo {
	infos := map[string]*imageInfo{}
	persisted := cloneImage(initial)
	persistSeg := map[string]int{}
	cache := Image{}
	seg := 0

	record := func() {
		keys := sortedKeys(cache)
		for mask := 0; mask < 1<<uint(len(keys)); mask++ {
			img := cloneImage(persisted)
			segOf := map[string]int{}
			for b := range persisted {
				if s, ok := persistSeg[b]; ok {
					segOf[b] = s
				}
			}
			for i, b := range keys {
				if mask>>uint(i)&1 == 1 {
					img[b] = cache[b]
					segOf[b] = seg
				}
			}
			key := imageKey(img)
			info, ok := infos[key]
			if !ok {
				info = &imageInfo{image: img, segMaps: map[string]map[string]int{}}
				infos[key] = info
			}
			info.segMaps[segKey(segOf)] = segOf
		}
	}

	record()
	for _, op := range ops {
		switch op.Kind {
		case OpFlush:
			for b, d := range cache {
				persisted[b] = d
				persistSeg[b] = seg
			}
			cache = Image{}
			seg++
		case OpWrite:
			if op.Force {
				persisted[op.Block] = op.Data
				persistSeg[op.Block] = seg
				delete(cache, op.Block)
			} else {
				cache[op.Block] = op.Data
			}
		}
		record()
	}
	return infos
}

func sortedKeys(img Image) []string {
	keys := make([]string, 0, len(img))
	for k := range img {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func imageKey(img Image) string {
	var sb strings.Builder
	for _, b := range sortedKeys(img) {
		sb.WriteString(b)
		sb.WriteByte('\x00')
		sb.WriteString(img[b])
		sb.WriteByte('\x01')
	}
	return sb.String()
}

func segKey(segOf map[string]int) string {
	blocks := make([]string, 0, len(segOf))
	for b := range segOf {
		blocks = append(blocks, b)
	}
	sort.Strings(blocks)
	var sb strings.Builder
	for _, b := range blocks {
		fmt.Fprintf(&sb, "%s\x00%d\x01", b, segOf[b])
	}
	return sb.String()
}

func cloneImage(img Image) Image {
	out := make(Image, len(img))
	for k, v := range img {
		out[k] = v
	}
	return out
}
