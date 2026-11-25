// Package spec 定义安全芯片回滚位置迁移问题的公共数据类型、
// 确定性序列化指纹以及可独立核验证书的序列化格式。
//
// 本包只包含纯数据结构与通用序列化工具，不包含任何迁移语义；
// 规划器与独立核验器分别自行实现语义，避免核验逻辑信任规划器。
package spec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

// Generation 是计数位置中某个签名范围的当前代次（防回滚烧录值）。
type Generation uint32

// OwnerID 是计数位置的稳定所有者标识。
type OwnerID string

// PositionID 是计数位置的稳定标识。
type PositionID string

// Scope 是签名范围标识；一个位置可覆盖一个或多个签名范围。
type Scope string

// Position 描述一个计数位置。
//
// 每个位置有稳定所有者 Owner 与固定签名范围集合 Scopes。
// Shadow=false 表示旧（遗留）位置，Shadow=true 表示迁移目标影子位置。
// 一个所有者可以拥有多个旧位置或多个影子位置，以表达共享位置分裂：
// 旧侧与影子侧覆盖的签名范围并集必须一致且互不重叠。
// Public 控制该位置的代次是否出现在公开读回中；指针状态从不出位。
type Position struct {
	ID     PositionID `json:"id"`
	Owner  OwnerID    `json:"owner"`
	Scopes []Scope    `json:"scopes"`
	Shadow bool       `json:"shadow"`
	Public bool       `json:"public"`
}

// Image 是一个已签名映像，携带它在各签名范围上的安全版本号。
// 映像在某条读取路径上被接受，当且仅当路径上每个位置的每个范围
// 的当前代次都不高于映像在该范围上的版本号。
type Image struct {
	ID       string               `json:"id"`
	Versions map[Scope]Generation `json:"versions"`
}

// RecoveryCombo 是一个指定恢复组合：为每个所有者的启动路径指定一个映像。
type RecoveryCombo struct {
	Images map[OwnerID]string `json:"images"`
}

// PointerPhase 是所有者切换指针的持久化阶段。
type PointerPhase string

const (
	// PhaseStable：指针稳定指向旧位置集合，仅旧路径可被固件选中。
	PhaseStable PointerPhase = "stable"
	// PhasePending：切换请求已持久化但确认尚未完成，旧路径与影子路径
	// 都可能被固件选中（掉电窗口）。
	PhasePending PointerPhase = "pending"
	// PhaseCommitted：指针已确认切换，仅影子路径可被固件选中。
	PhaseCommitted PointerPhase = "committed"
)

// Pointer 是单个所有者的切换指针状态。指针本身不可公开读回。
type Pointer struct {
	Phase PointerPhase `json:"phase"`
}

// State 是芯片的完整具体内部状态。
type State struct {
	// Counters[位置][签名范围] = 当前代次。
	Counters map[PositionID]map[Scope]Generation `json:"counters"`
	// Pointers[所有者] = 切换指针阶段。
	Pointers map[OwnerID]Pointer `json:"pointers"`
}

// Chip 是一颗待迁移芯片的静态规格与初始状态。
type Chip struct {
	// Positions 为全部计数位置（含旧位置与影子位置）。
	Positions []Position `json:"positions"`
	// Owners 为全部稳定所有者。
	Owners []OwnerID `json:"owners"`
	// Images 为规格中引用的全部映像。
	Images map[string]Image `json:"images"`
	// Revoked[所有者] 是该启动路径上原已撤销、必须始终被拒绝的映像。
	Revoked map[OwnerID]string `json:"revoked"`
	// Recovery 是全部指定恢复组合；任何状态下至少一个组合必须可启动。
	Recovery []RecoveryCombo `json:"recovery"`
	// Initial 是上电起点的具体内部状态。
	Initial *State `json:"initial"`
	// InitialAlternatives 是可选的额外上电初始具体状态。
	// 当芯片上电时存在宿主无法通过公开读回区分的隐藏内部不确定性，
	// 可在此列出多个候选；规划器把它们合并为同一个根信念，
	// 强制策略在“同一读回对应多种内部状态”下仍选择同一动作。
	InitialAlternatives []*State `json:"initial_alternatives,omitempty"`
	// MaxBurns 是单次升级会话的烧录预算 B；掉电重开后预算重置。
	MaxBurns int `json:"max_burns"`
}

// ActionKind 标识宿主可请求的持久动作类型。
type ActionKind string

const (
	// ActionBurn 烧录计数位置（每个目标范围单调升高），消耗一次烧录预算。
	ActionBurn ActionKind = "burn"
	// ActionRequest 持久化切换请求（与烧录是不同的持久动作）。
	ActionRequest ActionKind = "request_switch"
	// ActionConfirm 确认切换；请求与确认之间允许掉电。
	ActionConfirm ActionKind = "confirm_switch"
	// ActionYield 结束本会话并主动重启，不消耗烧录预算。
	ActionYield ActionKind = "yield"
)

// Action 是宿主按策略发出的一个动作。
//
// Kind=burn 时使用 Position 与 Gen（目标代次向量）；
// Kind=request_switch/confirm_switch 时使用 Owner；
// Kind=yield 时其余字段为空。
type Action struct {
	Kind     ActionKind           `json:"kind"`
	Owner    OwnerID              `json:"owner,omitempty"`
	Position PositionID           `json:"position,omitempty"`
	Gen      map[Scope]Generation `json:"gen,omitempty"`
}

// ObservationKind 是宿主能获得的观测类别。
type ObservationKind string

const (
	// ObsAck：动作被芯片确认，未发生掉电，不携带任何内部状态信息。
	ObsAck ObservationKind = "ack"
	// ObsBoot：掉电后（或主动 yield 后）重新启动，携带公开位置读回。
	ObsBoot ObservationKind = "boot"
)

// Observation 是宿主在动作之后获得的观测。
type Observation struct {
	Kind     ObservationKind                     `json:"kind"`
	Readback map[PositionID]map[Scope]Generation `json:"readback,omitempty"`
}

// Canonical 返回值的确定性紧凑 JSON 表示：递归排序所有 map 键，
// 使指纹与结构体字面量书写顺序无关。
func Canonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonical(&buf, reflect.ValueOf(v)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, rv reflect.Value) error {
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			buf.WriteString("null")
			return nil
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Struct:
		buf.WriteByte('{')
		rt := rv.Type()
		first := true
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if f.PkgPath != "" {
				continue
			}
			if !first {
				buf.WriteByte(',')
			}
			first = false
			name := f.Name
			if tag := f.Tag.Get("json"); tag != "" {
				name = splitJSONTag(tag)
			}
			key, _ := json.Marshal(name)
			buf.Write(key)
			buf.WriteByte(':')
			if err := writeCanonical(buf, rv.Field(i)); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case reflect.Map:
		keys := rv.MapKeys()
		type kv struct {
			key string
			val reflect.Value
		}
		pairs := make([]kv, 0, len(keys))
		for _, k := range keys {
			pairs = append(pairs, kv{key: fmt.Sprint(k.Interface()), val: rv.MapIndex(k)})
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].key < pairs[j].key })
		buf.WriteByte('{')
		for i, p := range pairs {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(p.key)
			if err != nil {
				return err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			if err := writeCanonical(buf, p.val); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case reflect.Slice, reflect.Array:
		buf.WriteByte('[')
		for i := 0; i < rv.Len(); i++ {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, rv.Index(i)); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	default:
		b, err := json.Marshal(rv.Interface())
		if err != nil {
			return err
		}
		buf.Write(b)
	}
	return nil
}

func splitJSONTag(tag string) string {
	for i := 0; i < len(tag); i++ {
		if tag[i] == ',' {
			return tag[:i]
		}
	}
	return tag
}

// Fingerprint 返回确定性 JSON 的 SHA-256 十六进制指纹。
func Fingerprint(v any) (string, error) {
	b, err := Canonical(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
