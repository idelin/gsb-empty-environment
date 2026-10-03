package deltacrdt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sort"
)

// DeltaEntry 是 Delta 包中的一个差异切片单元：一个字符节点的完整变更描述。
type DeltaEntry struct {
	ID        Dot
	Parent    Dot
	Value     rune
	Tombstone bool
	DeletedAt Dot // 仅 Tombstone 为 true 时有效
}

// Delta 是自某个确认水位以来的增量差异包。
// 全量状态传输等价于从零水位生成的 Delta。
type Delta struct {
	Entries []DeltaEntry
}

// GenerateDelta 生成自水位 since 之后发生变动的差异切片。
// 仅包含远端尚未确认的节点，包大小与变动量成正比，与文档总大小无关。
func (d *Document) GenerateDelta(since VersionVector) *Delta {
	delta := &Delta{}
	for _, n := range d.nodes {
		fresh := !since.Contains(n.id)
		freshDelete := n.tombstone && !since.Contains(n.deletedAt)
		if !fresh && !freshDelete {
			continue
		}
		delta.Entries = append(delta.Entries, DeltaEntry{
			ID:        n.id,
			Parent:    n.parent,
			Value:     n.value,
			Tombstone: n.tombstone,
			DeletedAt: n.deletedAt,
		})
	}
	// 排序保证编码确定性，便于测试与去重。
	sort.Slice(delta.Entries, func(i, j int) bool {
		return delta.Entries[i].ID.less(delta.Entries[j].ID)
	})
	return delta
}

// FullState 以零水位生成 Delta，即全量状态传输。
func (d *Document) FullState() *Delta {
	return d.GenerateDelta(VersionVector{})
}

// ApplyDelta 合并一个 Delta 包，返回应用后本地水位作为确认回执（Ack）。
func (d *Document) ApplyDelta(delta *Delta) VersionVector {
	for _, e := range delta.Entries {
		d.mergeNode(&node{
			id:        e.ID,
			parent:    e.Parent,
			value:     e.Value,
			tombstone: e.Tombstone,
			deletedAt: e.DeletedAt,
		})
		d.vv.Observe(e.ID)
		if e.Tombstone {
			d.vv.Observe(e.DeletedAt)
		}
	}
	return d.vv.Clone()
}

// entryOverhead: id(16) + parent(16) + value(4) + flags(1)
const entryOverhead = 37

// Encode 将 Delta 编码为紧凑二进制格式。
// 单字符变更的编码结果为常数级字节，与文档规模无关。
func (d *Delta) Encode() []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, uint32(len(d.Entries)))
	for _, e := range d.Entries {
		binary.Write(buf, binary.BigEndian, e.ID.Site)
		binary.Write(buf, binary.BigEndian, e.ID.Counter)
		binary.Write(buf, binary.BigEndian, e.Parent.Site)
		binary.Write(buf, binary.BigEndian, e.Parent.Counter)
		binary.Write(buf, binary.BigEndian, int32(e.Value))
		var flags byte
		if e.Tombstone {
			flags |= 1
		}
		buf.WriteByte(flags)
		if e.Tombstone {
			binary.Write(buf, binary.BigEndian, e.DeletedAt.Site)
			binary.Write(buf, binary.BigEndian, e.DeletedAt.Counter)
		}
	}
	return buf.Bytes()
}

// DecodeDelta 从二进制数据还原 Delta 包。
func DecodeDelta(data []byte) (*Delta, error) {
	buf := bytes.NewReader(data)
	var count uint32
	if err := binary.Read(buf, binary.BigEndian, &count); err != nil {
		return nil, err
	}
	delta := &Delta{Entries: make([]DeltaEntry, 0, count)}
	for i := uint32(0); i < count; i++ {
		var e DeltaEntry
		var value int32
		var flags byte
		if err := binary.Read(buf, binary.BigEndian, &e.ID.Site); err != nil {
			return nil, err
		}
		if err := binary.Read(buf, binary.BigEndian, &e.ID.Counter); err != nil {
			return nil, err
		}
		if err := binary.Read(buf, binary.BigEndian, &e.Parent.Site); err != nil {
			return nil, err
		}
		if err := binary.Read(buf, binary.BigEndian, &e.Parent.Counter); err != nil {
			return nil, err
		}
		if err := binary.Read(buf, binary.BigEndian, &value); err != nil {
			return nil, err
		}
		flagByte, err := buf.ReadByte()
		if err != nil {
			return nil, err
		}
		flags = flagByte
		e.Value = rune(value)
		e.Tombstone = flags&1 != 0
		if e.Tombstone {
			if err := binary.Read(buf, binary.BigEndian, &e.DeletedAt.Site); err != nil {
				return nil, err
			}
			if err := binary.Read(buf, binary.BigEndian, &e.DeletedAt.Counter); err != nil {
				return nil, err
			}
		}
		delta.Entries = append(delta.Entries, e)
	}
	if buf.Len() != 0 {
		return nil, errors.New("deltacrdt: trailing bytes in delta encoding")
	}
	return delta, nil
}
