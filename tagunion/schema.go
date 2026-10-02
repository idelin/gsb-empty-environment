package tagunion

import "fmt"

// 支持的序列化范围（显式定义，超出即拒绝）：
//   - 字段类型仅 Uint(uvarint)、Int(zigzag varint)、Bool、String、Bytes；
//   - 整数值域为 uint64 / int64 全范围；
//   - String 长度 ≤ MaxStringLen，Bytes 长度 ≤ MaxPayloadLen；
//   - 单对象载荷 ≤ MaxPayloadLen，批量对象数 ≤ MaxBatchCount；
//   - 不支持浮点、嵌套对象、指针/接口等宿主类型；解码不执行任何宿主代码，
//     仅做纯数据解析，且所有长度先验证再分配。
const (
	MaxPayloadLen = 1 << 20 // 单个对象载荷上限 1 MiB
	MaxStringLen  = 1 << 16 // 单个字符串字段上限 64 KiB
	MaxBatchCount = 1 << 16 // 批量对象数量上限
)

// FieldType 是字段的标量类型。
type FieldType int

const (
	FUint FieldType = iota
	FInt
	FBool
	FString
	FBytes
)

func (t FieldType) String() string {
	switch t {
	case FUint:
		return "uint"
	case FInt:
		return "int"
	case FBool:
		return "bool"
	case FString:
		return "string"
	case FBytes:
		return "bytes"
	}
	return "unknown"
}

// FieldDef 描述分支 schema 中的一个字段（编码按声明顺序）。
type FieldDef struct {
	Name string
	Type FieldType
}

// Schema 是某个标签在特定版本下的分支 schema。
type Schema struct {
	Tag     uint64
	Version uint64
	Fields  []FieldDef
}

// MigrateFunc 把某标签的字段集从版本 v 显式演进到 v+1。
// 演进必须显式注册，绝不用旧 schema 按巧合字段去解读新数据。
type MigrateFunc func(fields map[string]any) (map[string]any, error)

// Registry 持有全部 (tag, version) -> schema 与显式演进映射。
type Registry struct {
	schemas    map[[2]uint64]*Schema
	migrations map[uint64]map[uint64]MigrateFunc // tag -> fromVersion -> migrate
	latest     map[uint64]uint64
}

func NewRegistry() *Registry {
	return &Registry{
		schemas:    make(map[[2]uint64]*Schema),
		migrations: make(map[uint64]map[uint64]MigrateFunc),
		latest:     make(map[uint64]uint64),
	}
}

// Register 注册一个分支 schema。空 Fields 表示合法的空分支。
func (r *Registry) Register(s Schema) error {
	key := [2]uint64{s.Tag, s.Version}
	if _, dup := r.schemas[key]; dup {
		return fmt.Errorf("schema tag=%d version=%d already registered", s.Tag, s.Version)
	}
	seen := make(map[string]bool, len(s.Fields))
	for _, f := range s.Fields {
		if f.Name == "" {
			return fmt.Errorf("schema tag=%d version=%d: empty field name", s.Tag, s.Version)
		}
		if seen[f.Name] {
			return fmt.Errorf("schema tag=%d version=%d: duplicate field %q", s.Tag, s.Version, f.Name)
		}
		seen[f.Name] = true
	}
	cp := s
	cp.Fields = append([]FieldDef(nil), s.Fields...)
	r.schemas[key] = &cp
	if v, ok := r.latest[s.Tag]; !ok || s.Version > v {
		r.latest[s.Tag] = s.Version
	}
	return nil
}

// RegisterMigration 注册 tag 从 fromVersion 到 fromVersion+1 的显式演进函数。
func (r *Registry) RegisterMigration(tag, fromVersion uint64, fn MigrateFunc) error {
	if fn == nil {
		return fmt.Errorf("nil migration for tag=%d from=%d", tag, fromVersion)
	}
	m := r.migrations[tag]
	if m == nil {
		m = make(map[uint64]MigrateFunc)
		r.migrations[tag] = m
	}
	if _, dup := m[fromVersion]; dup {
		return fmt.Errorf("migration tag=%d from=%d already registered", tag, fromVersion)
	}
	m[fromVersion] = fn
	return nil
}

func (r *Registry) schema(tag, version uint64) (*Schema, bool) {
	s, ok := r.schemas[[2]uint64{tag, version}]
	return s, ok
}

// knownTag 报告该标签是否有任一已注册版本。
func (r *Registry) knownTag(tag uint64) bool {
	_, ok := r.latest[tag]
	return ok
}

// migrateToLatest 沿显式演进链把字段集升级到最新版本，返回新版本号。
func (r *Registry) migrateToLatest(tag, version uint64, fields map[string]any) (uint64, map[string]any, error) {
	for version < r.latest[tag] {
		fn, ok := r.migrations[tag][version]
		if !ok {
			return 0, nil, fmt.Errorf("tag=%d: no explicit migration from version %d to %d", tag, version, version+1)
		}
		next, err := fn(fields)
		if err != nil {
			return 0, nil, fmt.Errorf("tag=%d: migration v%d->v%d: %w", tag, version, version+1, err)
		}
		fields = next
		version++
	}
	return version, fields, nil
}
