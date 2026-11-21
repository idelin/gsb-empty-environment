package commit

import "fmt"

// Device 是物理底盘的最小接口。提交期间只允许按 Plan 中列出的动作调用，
// 任何计划外的盘面访问都视为违规（由 SpyDevice 在测试中拦截）。
type Device interface {
	Read(sector SectorID) ([]byte, error)
	Write(sector SectorID, payload []byte) error
}

// MapDevice 是驻留内存的参考底盘，用于记录器重放与测试。
type MapDevice struct {
	Sectors map[SectorID][]byte
}

func (m *MapDevice) Read(sector SectorID) ([]byte, error) {
	data, ok := m.Sectors[sector]
	if !ok {
		return nil, fmt.Errorf("commit: read unknown sector %d", sector)
	}
	return data, nil
}

func (m *MapDevice) Write(sector SectorID, payload []byte) error {
	if _, ok := m.Sectors[sector]; !ok {
		return fmt.Errorf("commit: write unknown sector %d", sector)
	}
	dup := make([]byte, len(payload))
	copy(dup, payload)
	m.Sectors[sector] = dup
	return nil
}

// Recorder 按拍重放计划并核对全部物理约束。
//
// 核对项：槽位占用（读装空槽、释放腾槽、写不改槽位）；读的必须是未覆盖入口扇区；
// 写时配方的全部引用源同时驻留；磁头随读/写移动、释放不动磁头；读成本逐拍累加
// （int64 不截断）；写入负载严格由驻留源切片与常量组装；每个目标恰写一次；
// 最终盘与逐目标期望完全一致。
type Recorder struct {
	inst    *Instance
	dev     Device
	head    Cylinder
	slots   map[SectorID][]byte
	over    map[SectorID]bool
	written map[SectorID]bool

	ReadCost int64
	Reads    int
	Writes   int
	Releases int
	Ticks    int
	Log      []Tick
}

// Tick 是一拍的完整观测快照。
type Tick struct {
	Index    int
	Op       Op
	HeadFrom Cylinder
	HeadTo   Cylinder
	Occupied []SectorID
	ReadCost int64
}

func NewRecorder(inst *Instance, dev Device) *Recorder {
	return &Recorder{
		inst:    inst,
		dev:     dev,
		head:    inst.Initial,
		slots:   map[SectorID][]byte{},
		over:    map[SectorID]bool{},
		written: map[SectorID]bool{},
	}
}
