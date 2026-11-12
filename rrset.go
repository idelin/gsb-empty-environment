package netsec

import (
	"errors"
	"fmt"
	"time"
)

// Record 是一条资源记录。同一集合内的记录必须共享名称、类型与类别。
type Record struct {
	Name  string
	Type  uint16
	Class uint16
	TTL   uint32
	Data  string
}

// Key 唯一标识一个资源记录集合：名称、类型与类别。
type Key struct {
	Name  string
	Type  uint16
	Class uint16
}

// Key 返回记录所属集合的键。
func (r Record) Key() Key {
	return Key{Name: r.Name, Type: r.Type, Class: r.Class}
}

// TrustLevel 表示解析结果的验证（信任）级别，数值越大信任越高。
type TrustLevel int

const (
	// TrustNone 未做任何验证。
	TrustNone TrustLevel = iota
	// TrustInsecure 已确认链路上无签名（不安全的委托）。
	TrustInsecure
	// TrustSecure 签名验证通过。
	TrustSecure
)

func (t TrustLevel) String() string {
	switch t {
	case TrustNone:
		return "none"
	case TrustInsecure:
		return "insecure"
	case TrustSecure:
		return "secure"
	default:
		return fmt.Sprintf("trust(%d)", int(t))
	}
}

var (
	// ErrEmptySet 表示尝试写入空集合。
	ErrEmptySet = errors.New("netsec: empty record set")
	// ErrMixedSet 表示集合内记录的名称、类型或类别不一致。
	ErrMixedSet = errors.New("netsec: records do not share name, type and class")
	// ErrTrustDowngrade 表示低信任结果试图覆盖仍有效的高信任组。
	ErrTrustDowngrade = errors.New("netsec: refusing to overwrite a still-valid higher-trust set")
)

// group 是缓存中按整组保存的集合及其元数据。
type group struct {
	records   []Record
	trust     TrustLevel
	expiresAt time.Time
}

// expired 报告该组在 now 时刻是否已到期。
func (g *group) expired(now time.Time) bool {
	return !now.Before(g.expiresAt)
}

// normalize 校验集合同名同类，去重完全相同的记录，并返回整组到期所需的最小 TTL。
func normalize(records []Record) (Key, []Record, uint32, error) {
	if len(records) == 0 {
		return Key{}, nil, 0, ErrEmptySet
	}
	key := records[0].Key()
	seen := make(map[Record]struct{}, len(records))
	deduped := make([]Record, 0, len(records))
	minTTL := records[0].TTL
	for _, r := range records {
		if r.Key() != key {
			return Key{}, nil, 0, fmt.Errorf("%w: %q/%d/%d vs %q/%d/%d",
				ErrMixedSet,
				key.Name, key.Type, key.Class,
				r.Name, r.Type, r.Class)
		}
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		deduped = append(deduped, r)
		if r.TTL < minTTL {
			minTTL = r.TTL
		}
	}
	return key, deduped, minTTL, nil
}
