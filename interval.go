package trainsim

// Time 是离散仿真时钟节拍。
type Time int64

// Interval 是半开占用区间 [Start, End)。
// 两个仅端点相接的区间（a.End == b.Start）不算重叠，
// 因此轨段在时刻 t 释放后，另一列车可在同一时刻 t 进入。
type Interval struct {
	Start Time
	End   Time
}

// Overlaps 报告两个半开区间是否共享任何节拍。
func (a Interval) Overlaps(b Interval) bool {
	return a.Start < b.End && b.Start < a.End
}

// Len 返回区间时长。
func (a Interval) Len() Time { return a.End - a.Start }
