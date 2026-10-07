package trainsim

import "fmt"

// SegmentID 标识一段轨道。
type SegmentID string

// TrainID 标识一列列车。
type TrainID string

// Segment 是一段轨道：有最小走行时间；
// 只有车站（Station）轨段允许停车等待（延长占用）。
type Segment struct {
	ID      SegmentID
	Travel  Time
	Station bool
}

// Network 是静态的轨段集合。
type Network struct {
	segs map[SegmentID]Segment
}

// NewNetwork 由轨段列表构建网络。
func NewNetwork(segs []Segment) *Network {
	n := &Network{segs: make(map[SegmentID]Segment, len(segs))}
	for _, s := range segs {
		n.segs[s.ID] = s
	}
	return n
}

// Segment 查询轨段定义。
func (n *Network) Segment(id SegmentID) (Segment, bool) {
	s, ok := n.segs[id]
	return s, ok
}

func (n *Network) mustSegment(id SegmentID) Segment {
	s, ok := n.segs[id]
	if !ok {
		panic(fmt.Sprintf("unknown segment %q", id))
	}
	return s
}
