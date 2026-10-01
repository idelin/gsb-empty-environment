package wire

import "fmt"

// FieldDef 描述一个字段：稳定编号、逻辑类型、是否必填。
// 编号一旦发布不得更改含义；删除字段时应对编号调用 Schema.Reserve。
type FieldDef struct {
	Num      int
	Type     FieldType
	Required bool
}

// Schema 是某一代消息的布局定义。Version 随可选字段新增而递增，
// 写入数据帧头，解码端据此给出版本诊断。
type Schema struct {
	Name     string
	Version  uint32
	fields   map[int]FieldDef
	reserved map[int]string // 已删除编号 -> 删除原因
}

func NewSchema(name string, version uint32) *Schema {
	return &Schema{
		Name:     name,
		Version:  version,
		fields:   make(map[int]FieldDef),
		reserved: make(map[int]string),
	}
}

// Reserve 永久保留一个已删除的字段编号，防止其被复用为不相关类型。
func (s *Schema) Reserve(num int, reason string) error {
	if num <= 0 {
		return fmt.Errorf("field number must be positive, got %d", num)
	}
	if _, ok := s.fields[num]; ok {
		return fmt.Errorf("field %d is still in use, cannot reserve", num)
	}
	s.reserved[num] = reason
	return nil
}

// AddField 注册字段。编号重复、编号被保留、类型非法都会报错。
func (s *Schema) AddField(def FieldDef) error {
	if def.Num <= 0 {
		return fmt.Errorf("field number must be positive, got %d", def.Num)
	}
	if def.Type < TUint64 || def.Type > TBytes {
		return fmt.Errorf("field %d: unsupported type %d", def.Num, int(def.Type))
	}
	if reason, ok := s.reserved[def.Num]; ok {
		return fmt.Errorf("field number %d is reserved (deleted: %s); reuse for an unrelated type is forbidden", def.Num, reason)
	}
	if prev, ok := s.fields[def.Num]; ok {
		return fmt.Errorf("field number %d already defined as %s, conflicting redefinition as %s rejected", def.Num, prev.Type, def.Type)
	}
	s.fields[def.Num] = def
	return nil
}

// MustAdd 是 AddField 的便捷形式，模式错误时 panic（适用于包级声明）。
func (s *Schema) MustAdd(def FieldDef) *Schema {
	if err := s.AddField(def); err != nil {
		panic(err)
	}
	return s
}

func (s *Schema) field(num int) (FieldDef, bool) {
	def, ok := s.fields[num]
	return def, ok
}
