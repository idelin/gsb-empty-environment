package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// 校验失败时返回的哨兵错误，调用方可用 errors.Is 判定具体原因。
var (
	// ErrZeroCount 表示映射区间长度为 0。
	ErrZeroCount = errors.New("idmap: mapping count must be greater than zero")
	// ErrRangeOverflow 表示 first+count 超过 uint32 可表达的范围（整数回绕）。
	ErrRangeOverflow = errors.New("idmap: first+count overflows uint32")
	// ErrRangeOutsideAllowed 表示映射使用的主机 ID 不在请求者被授权的范围内。
	ErrRangeOutsideAllowed = errors.New("idmap: mapping outside requester's allowed id range")
	// ErrOverlappingMapping 表示两条映射在同一侧命名空间内占用了重叠的 ID 区间。
	ErrOverlappingMapping = errors.New("idmap: mapping entries overlap")
	// ErrRootMappingNeedsNosuid 表示非初始用户命名空间映射到 root 却未要求 nosuid。
	ErrRootMappingNeedsNosuid = errors.New("idmap: non-initial namespace mapping root requires nosuid")
	// ErrUnmappedID 表示待翻译的 ID 没有落在任何映射区间内。
	ErrUnmappedID = errors.New("idmap: id is not covered by any mapping entry")
	// ErrInvalidDirection 表示调用方传入了未定义的翻译方向。
	ErrInvalidDirection = errors.New("idmap: unknown translation direction")
	// ErrInvalidCapabilityXattr 表示 security.capability 的值格式无法识别。
	ErrInvalidCapabilityXattr = errors.New("idmap: malformed security.capability xattr value")
)

// IDRange 是半开区间 [First, First+Count)。
type IDRange struct {
	First uint32
	Count uint32
}

// IDMapEntry 描述两个用户命名空间之间一段连续 ID 的一一对应关系。
//
// ContainerID 是请求者（非初始）用户命名空间内的起始 ID，
// HostID 是主机初始用户命名空间内的起始 ID，二者通过相同偏移互译。
type IDMapEntry struct {
	ContainerID uint32
	HostID      uint32
	Count       uint32
}

// RequesterRange 是挂载服务为请求者分配的主机侧 uid/gid 授权区间。
type RequesterRange struct {
	UIDs IDRange
	GIDs IDRange
}

// MountFlags 是挂载服务关心的 mount(2) 标志位子集，取值与 Linux 内核一致。
type MountFlags uint64

const (
	// MountFlagNosuid 对应 Linux MS_NOSUID：禁止 setuid/setgid 与文件能力生效。
	MountFlagNosuid MountFlags = 1 << 1
	// MountFlagNoDev 对应 Linux MS_NODEV。
	MountFlagNoDev MountFlags = 1 << 9
	// MountFlagNoExec 对应 Linux MS_NOEXEC。
	MountFlagNoExec MountFlags = 1 << 15
)

// Nosuid 报告是否设置了 MS_NOSUID。
func (f MountFlags) Nosuid() bool { return f&MountFlagNosuid != 0 }

// Direction 表示 ID 翻译方向。
type Direction int

const (
	// ToHost 把请求者命名空间内的 ID 翻译为主机初始命名空间 ID。
	ToHost Direction = iota + 1
	// ToContainer 把主机初始命名空间 ID 翻译为请求者命名空间内的 ID。
	ToContainer
)

// XAttrNameSecurityCapability 是承载文件能力的 xattr 名称。
const XAttrNameSecurityCapability = "security.capability"

const uint32Boundary = uint64(1) << 32

// rangeEnd 返回半开区间的排他上界，统一用 uint64 运算以杜绝回绕。
func rangeEnd(first, count uint32) (uint64, error) {
	if count == 0 {
		return 0, ErrZeroCount
	}
	end := uint64(first) + uint64(count)
	if end > uint32Boundary {
		return 0, fmt.Errorf("%w: [%d, %d)", ErrRangeOverflow, first, count)
	}
	return end, nil
}

func rangesOverlap(startA, endA, startB, endB uint64) bool {
	return startA < endB && startB < endA
}

func rangeWithin(start, end, allowedStart, allowedEnd uint64) bool {
	return start >= allowedStart && end <= allowedEnd
}

// ValidateIDMap 校验一组映射条目：
//   - 每条映射长度非零，且 first+count 不会越过 uint32 上界；
//   - 每条映射占用的主机侧区间必须完整落在请求者被授权的 allowed 内；
//   - 任意两条映射在容器侧、主机侧都不得重叠（首尾相接允许）。
//
// 函数不会修改 entries。
func ValidateIDMap(entries []IDMapEntry, allowed IDRange) error {
	if len(entries) == 0 {
		if allowed.Count == 0 {
			return nil
		}
		_, err := rangeEnd(allowed.First, allowed.Count)
		return err
	}

	allowedEnd, err := rangeEnd(allowed.First, allowed.Count)
	if err != nil {
		return fmt.Errorf("allowed range: %w", err)
	}

	containerEnds := make([]uint64, len(entries))
	hostEnds := make([]uint64, len(entries))

	for i, entry := range entries {
		containerEnd, err := rangeEnd(entry.ContainerID, entry.Count)
		if err != nil {
			return fmt.Errorf("entry %d container side: %w", i, err)
		}
		hostEnd, err := rangeEnd(entry.HostID, entry.Count)
		if err != nil {
			return fmt.Errorf("entry %d host side: %w", i, err)
		}

		if !rangeWithin(uint64(entry.HostID), hostEnd, uint64(allowed.First), allowedEnd) {
			return fmt.Errorf("%w: entry %d claims host ids [%d,%d), allowed [%d,%d)",
				ErrRangeOutsideAllowed, i, entry.HostID, hostEnd, allowed.First, allowedEnd)
		}

		for j := 0; j < i; j++ {
			if rangesOverlap(uint64(entry.ContainerID), containerEnd,
				uint64(entries[j].ContainerID), containerEnds[j]) {
				return fmt.Errorf("%w: entries %d and %d overlap on container ids",
					ErrOverlappingMapping, j, i)
			}
			if rangesOverlap(uint64(entry.HostID), hostEnd,
				uint64(entries[j].HostID), hostEnds[j]) {
				return fmt.Errorf("%w: entries %d and %d overlap on host ids",
					ErrOverlappingMapping, j, i)
			}
		}

		containerEnds[i] = containerEnd
		hostEnds[i] = hostEnd
	}
	return nil
}

// MountRequest 是挂载服务收到的 idmap 挂载请求中与安全校验相关的部分。
type MountRequest struct {
	// InitialNamespace 为 true 表示请求者位于初始用户命名空间，
	// 此时映射的 root 就是主机真实 root，不触发 nosuid 强制要求。
	InitialNamespace bool
	// Flags 为挂载标志位，非初始命名空间映射 root 时必须包含 MountFlagNosuid。
	Flags MountFlags
	// UIDMap / GIDMap 为请求附带的 uid/gid 映射条目。
	UIDMap []IDMapEntry
	GIDMap []IDMapEntry
}

// ValidateMount 执行挂载级校验：uid/gid 映射本身必须合法，且当请求者处于
// 非初始用户命名空间、又把容器内 uid 0（root）映射出来时，挂载必须带
// MS_NOSUID，以防伪造的 setuid 程序或文件能力在映射外的主机进程上提权。
func ValidateMount(req MountRequest, allowed RequesterRange) error {
	if err := ValidateIDMap(req.UIDMap, allowed.UIDs); err != nil {
		return fmt.Errorf("uid map: %w", err)
	}
	if err := ValidateIDMap(req.GIDMap, allowed.GIDs); err != nil {
		return fmt.Errorf("gid map: %w", err)
	}

	if req.InitialNamespace || req.Flags.Nosuid() {
		return nil
	}
	for _, entry := range req.UIDMap {
		if entry.ContainerID == 0 {
			return fmt.Errorf("%w: container uid 0 mapped to host uid %d without nosuid",
				ErrRootMappingNeedsNosuid, entry.HostID)
		}
	}
	return nil
}

// TranslateID 按 entries 在两个命名空间之间翻译单个 ID。
// 条目应先通过 ValidateIDMap 校验；未被任何区间覆盖的 ID 返回 ErrUnmappedID，
// 绝不静默返回原值。
func TranslateID(entries []IDMapEntry, id uint32, direction Direction) (uint32, error) {
	for _, entry := range entries {
		switch direction {
		case ToHost:
			if idWithin(id, entry.ContainerID, entry.Count) {
				translated := uint64(entry.HostID) + uint64(id-entry.ContainerID)
				return uint32(translated), nil
			}
		case ToContainer:
			if idWithin(id, entry.HostID, entry.Count) {
				translated := uint64(entry.ContainerID) + uint64(id-entry.HostID)
				return uint32(translated), nil
			}
		default:
			return 0, fmt.Errorf("%w: %d", ErrInvalidDirection, direction)
		}
	}
	return 0, fmt.Errorf("%w: %d (direction %d)", ErrUnmappedID, id, direction)
}

func idWithin(id, first, count uint32) bool {
	return uint64(id) >= uint64(first) && uint64(id) < uint64(first)+uint64(count)
}

// Linux security.capability 磁盘布局相关常量，取值与内核 vfs_ns_cap_data 一致。
const (
	vfsCapRevisionMask  = uint32(0xFF000000)
	vfsCapFlagsMask     = ^vfsCapRevisionMask
	vfsCapFlagEffective = uint32(0x00000001)

	vfsCapRevision1 = uint32(0x01000000)
	vfsCapRevision2 = uint32(0x02000000)
	vfsCapRevision3 = uint32(0x03000000)

	// vfs_cap_data：magic_etc + 两组 (permitted, inheritable)，共 20 字节，无 rootid。
	vfsCapDataSize = 20
	// vfs_ns_cap_data：在 vfs_cap_data 之后追加 4 字节 rootid，共 24 字节。
	vfsNsCapDataSize     = 24
	vfsNsCapRootIDOffset = 20
)

// TranslateCapabilityRootID 翻译 security.capability xattr 值中的 rootid。
//
// revision 1/2 的旧格式不含 rootid，原样返回一份拷贝；revision 3 的
// vfs_ns_cap_data 在偏移 20 处携带 rootid，必须随 idmap 一起翻译，
// 否则容器内伪造的能力会被记到映射外的主机 uid 名下。
//
// 入参 value 不会被修改；rootid 未被映射覆盖时返回 ErrUnmappedID。
func TranslateCapabilityRootID(entries []IDMapEntry, value []byte, direction Direction) ([]byte, error) {
	switch len(value) {
	case vfsCapDataSize, vfsNsCapDataSize:
	default:
		return nil, fmt.Errorf("%w: length %d is neither %d nor %d bytes",
			ErrInvalidCapabilityXattr, len(value), vfsCapDataSize, vfsNsCapDataSize)
	}

	magic := binary.LittleEndian.Uint32(value[0:4])
	revision := magic & vfsCapRevisionMask
	if magic&vfsCapFlagsMask&^vfsCapFlagEffective != 0 {
		return nil, fmt.Errorf("%w: unknown capability flags 0x%x",
			ErrInvalidCapabilityXattr, magic&vfsCapFlagsMask)
	}

	switch revision {
	case vfsCapRevision1, vfsCapRevision2:
		if len(value) != vfsCapDataSize {
			return nil, fmt.Errorf("%w: revision 0x%x expects %d bytes, got %d",
				ErrInvalidCapabilityXattr, revision, vfsCapDataSize, len(value))
		}
		out := make([]byte, len(value))
		copy(out, value)
		return out, nil
	case vfsCapRevision3:
		if len(value) != vfsNsCapDataSize {
			return nil, fmt.Errorf("%w: revision 3 expects %d bytes, got %d",
				ErrInvalidCapabilityXattr, vfsNsCapDataSize, len(value))
		}
	default:
		return nil, fmt.Errorf("%w: unknown revision 0x%x", ErrInvalidCapabilityXattr, revision)
	}

	rootID := binary.LittleEndian.Uint32(value[vfsNsCapRootIDOffset:])
	translated, err := TranslateID(entries, rootID, direction)
	if err != nil {
		return nil, fmt.Errorf("capability rootid: %w", err)
	}

	out := make([]byte, len(value))
	copy(out, value)
	binary.LittleEndian.PutUint32(out[vfsNsCapRootIDOffset:], translated)
	return out, nil
}

// TranslateXAttr 是挂载服务处理 xattr 透传时的统一入口：
// security.capability 需要翻译 rootid，其余 xattr 与 ID 映射无关，原样拷贝返回。
func TranslateXAttr(name string, value []byte, entries []IDMapEntry, direction Direction) ([]byte, error) {
	if name != XAttrNameSecurityCapability {
		out := make([]byte, len(value))
		copy(out, value)
		return out, nil
	}
	return TranslateCapabilityRootID(entries, value, direction)
}
