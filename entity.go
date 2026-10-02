package hsengine

// Entity 一个动态实体（如空中交通单元）。
// 假定在预测时窗内做匀速直线运动。
type Entity struct {
	ID       string
	Position Vector3
	Velocity Vector3
}

// PositionAt 推演 t 秒后的位置。
func (e *Entity) PositionAt(t float64) Vector3 {
	return e.Position.Add(e.Velocity.Scale(t))
}

// Distance 两实体当前间距。
func Distance(a, b *Entity) float64 {
	return a.Position.Distance(b.Position)
}
