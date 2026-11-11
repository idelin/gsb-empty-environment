// Package netsec 提供多层隧道流量的层级解码能力。
//
// 调用方（Go 流量宿主）为每一层提供解码回调，回调返回本层载荷区间
// 以及未经验证的元数据。包内保证：
//   - 只有受信入口映射（EntryMapper）能为事件赋予租户身份；
//   - 内层元数据中的租户字段仅作为证据记录，绝不提升为身份；
//   - 解码深度、累计解码字节数受限；
//   - 每层载荷区间必须合法且包含于上一层载荷之内。
//
// 解码器不保存任何跨事件状态，单个事件被拒绝不会污染后续事件。
package netsec

import (
	"errors"
	"fmt"
)

// 解码被拒绝时返回的哨兵错误，可用 errors.Is 判定。
var (
	// ErrDepthLimit 表示解码层级超过 Limits.MaxDepth。
	ErrDepthLimit = errors.New("netsec: decode depth limit exceeded")
	// ErrByteLimit 表示累计解码字节超过 Limits.MaxDecodedBytes。
	ErrByteLimit = errors.New("netsec: cumulative decoded byte limit exceeded")
	// ErrRangeOutOfBounds 表示回调返回的载荷区间越界或自相矛盾。
	ErrRangeOutOfBounds = errors.New("netsec: payload range out of bounds")
	// ErrIdentityConflict 表示内层元数据中的租户声明与受信入口身份冲突。
	ErrIdentityConflict = errors.New("netsec: inner tenant claim conflicts with trusted entry identity")
)

// TenantClaimKey 是元数据中可能被用来声明租户身份的键。
// 该字段在任何层级都只作为证据，只有入口层经 EntryMapper 解析后才成为身份。
const TenantClaimKey = "tenant_id"

// Range 描述载荷在当前层缓冲区中的半开字节区间 [Start, End)。
type Range struct {
	Start int
	End   int
}

// Len 返回区间长度。
func (r Range) Len() int { return r.End - r.Start }

// Layer 是单层解码回调的返回结果。
type Layer struct {
	// Payload 指向下一段待解码载荷在当前输入缓冲区中的区间。
	Payload Range
	// Metadata 是本层未经验证的元数据，任何字段都不被视为身份。
	Metadata map[string]string
}

// LayerDecoder 由调用方提供，解封装一层隧道。
// 输入 payload 是上一层（或入口报文）的载荷字节。
type LayerDecoder func(payload []byte) (Layer, error)

// EntryMapper 是受信入口映射：仅作用于第 0 层（入口层）元数据，
// 返回该事件可信的租户身份。其它层级不会经过该映射。
type EntryMapper func(entryMetadata map[string]string) (tenantID string, ok bool)

// Limits 约束单次事件解码的资源消耗。
type Limits struct {
	// MaxDepth 是允许解码的最大层数；层数超过该值的事件被拒绝。
	MaxDepth int
	// MaxDecodedBytes 是单次事件累计提交给解码回调的字节数上限。
	MaxDecodedBytes int64
}

// LayerEvidence 记录单层解码留下的证据。
type LayerEvidence struct {
	Depth    int
	Payload  Range
	Metadata map[string]string
}

// Event 是一次成功解码的结果。
type Event struct {
	// TenantID 仅由受信入口映射赋予；无映射命中时为空。
	TenantID string
	// Layers 按深度记录各层证据，内层租户声明只会出现在这里。
	Layers []LayerEvidence
	// DecodedBytes 是本次事件累计提交给解码回调的字节数。
	DecodedBytes int64
}

// Decoder 对单条报文按层解封装。Decoder 不保存跨事件状态，
// 可安全地并发用于多个事件。
type Decoder struct {
	limits Limits
	mapper EntryMapper
	layers []LayerDecoder
}

// NewDecoder 构造解码器。layers 按从外到内的顺序提供，
// 第 0 层即入口层。mapper 为 nil 时事件不赋予租户身份。
func NewDecoder(limits Limits, mapper EntryMapper, layers ...LayerDecoder) *Decoder {
	return &Decoder{limits: limits, mapper: mapper, layers: layers}
}

// Decode 对 data 执行层级解码。任何一层校验失败都会拒绝整个事件，
// 且不会影响本解码器处理的后续事件。
func (d *Decoder) Decode(data []byte) (*Event, error) {
	if d.limits.MaxDepth >= 0 && len(d.layers) > d.limits.MaxDepth {
		return nil, fmt.Errorf("%w: %d layers configured, max %d", ErrDepthLimit, len(d.layers), d.limits.MaxDepth)
	}

	ev := &Event{Layers: make([]LayerEvidence, 0, len(d.layers))}
	current := data

	for depth, decode := range d.layers {
		if d.limits.MaxDecodedBytes >= 0 && ev.DecodedBytes+int64(len(current)) > d.limits.MaxDecodedBytes {
			return nil, fmt.Errorf("%w: depth %d would reach %d bytes, max %d",
				ErrByteLimit, depth, ev.DecodedBytes+int64(len(current)), d.limits.MaxDecodedBytes)
		}
		ev.DecodedBytes += int64(len(current))

		layer, err := decode(current)
		if err != nil {
			return nil, fmt.Errorf("netsec: depth %d decode: %w", depth, err)
		}

		if err := validateRange(layer.Payload, len(current)); err != nil {
			return nil, fmt.Errorf("netsec: depth %d: %w", depth, err)
		}

		// 防御性拷贝：回调返回的元数据可能被复用或事后篡改。
		meta := make(map[string]string, len(layer.Metadata))
		for k, v := range layer.Metadata {
			meta[k] = v
		}

		if depth == 0 {
			if d.mapper != nil {
				if tenant, ok := d.mapper(meta); ok {
					ev.TenantID = tenant
				}
			}
		} else if claim, ok := meta[TenantClaimKey]; ok && ev.TenantID != "" && claim != ev.TenantID {
			return nil, fmt.Errorf("%w: depth %d claims %q, trusted entry identity is %q",
				ErrIdentityConflict, depth, claim, ev.TenantID)
		}

		ev.Layers = append(ev.Layers, LayerEvidence{Depth: depth, Payload: layer.Payload, Metadata: meta})
		// 区间包含关系：下一层载荷必为当前层载荷的子区间。
		current = current[layer.Payload.Start:layer.Payload.End]
	}

	return ev, nil
}

// validateRange 校验区间合法且包含于长度为 n 的父载荷之内。
func validateRange(r Range, n int) error {
	if r.Start < 0 || r.End < r.Start || r.End > n {
		return fmt.Errorf("%w: [%d, %d) within %d bytes", ErrRangeOutOfBounds, r.Start, r.End, n)
	}
	return nil
}
