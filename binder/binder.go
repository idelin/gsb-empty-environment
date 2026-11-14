// Package binder 提供供账户服务调用的可导入字段绑定组件。
//
// 每个接口通过 Schema 声明可写字段白名单、只读字段与服务器生成字段。
// 客户端绑定（BindClient）只复制白名单内且类型正确的值；未知字段、
// 嵌套对象与大小写变体一律拒绝并记录。角色、租户、额度与审计等
// 服务器字段只能经由 ApplyServer 在独立授权下写入。
package binder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
)

// 拒绝原因。
const (
	ReasonUnknownField  = "unknown_field"
	ReasonCaseVariant   = "case_variant"
	ReasonReadOnly      = "read_only"
	ReasonServerOnly    = "server_only"
	ReasonNullValue     = "null_overwrite"
	ReasonNestedObject  = "nested_object"
	ReasonTypeMismatch  = "type_mismatch"
	ReasonNotAuthorized = "not_authorized"
)

// Rejection 记录一次被拒绝或忽略的字段写入。
type Rejection struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// Result 汇总一次绑定的结果。
type Result struct {
	Applied   []string    `json:"applied"`
	Rejected  []Rejection `json:"rejected"`
	ServerSet []string    `json:"server_set,omitempty"`
}

// Schema 声明单个接口的字段策略。字段名一律使用 JSON 键名，
// 匹配区分大小写。
type Schema struct {
	// Name 接口名，用于日志。
	Name string
	// Writable 客户端可写字段白名单。
	Writable []string
	// ReadOnly 客户端可见但不可写的字段。
	ReadOnly []string
	// ServerOnly 只能由服务端流程写入的字段（角色、租户、额度、审计等）。
	ServerOnly []string

	logger *slog.Logger
}

// Option 自定义 Schema 行为。
type Option func(*Schema)

// WithLogger 设置审计日志记录器，默认使用 slog.Default()。
func WithLogger(l *slog.Logger) Option {
	return func(s *Schema) { s.logger = l }
}

// NewSchema 构造一个字段策略。
func NewSchema(name string, writable, readOnly, serverOnly []string, opts ...Option) *Schema {
	s := &Schema{
		Name:       name,
		Writable:   append([]string(nil), writable...),
		ReadOnly:   append([]string(nil), readOnly...),
		ServerOnly: append([]string(nil), serverOnly...),
		logger:     slog.Default(),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Schema) writableSet() map[string]bool {
	set := make(map[string]bool, len(s.Writable))
	for _, f := range s.Writable {
		set[f] = true
	}
	return set
}

func (s *Schema) knownFieldCaseFold(key string) (string, bool) {
	for _, group := range [][]string{s.Writable, s.ReadOnly, s.ServerOnly} {
		for _, f := range group {
			if strings.EqualFold(f, key) {
				return f, true
			}
		}
	}
	return "", false
}

func (s *Schema) classify(key string) string {
	for _, f := range s.ReadOnly {
		if f == key {
			return ReasonReadOnly
		}
	}
	for _, f := range s.ServerOnly {
		if f == key {
			return ReasonServerOnly
		}
	}
	if _, ok := s.knownFieldCaseFold(key); ok {
		return ReasonCaseVariant
	}
	return ReasonUnknownField
}

// fieldIndex 建立 JSON 键名到结构体字段的精确映射（区分大小写，
// 不使用 encoding/json 的大小写不敏感匹配）。
func fieldIndex(dst any) (map[string]reflect.Value, error) {
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return nil, errors.New("binder: dst must be a non-nil pointer to struct")
	}
	root := rv.Elem()
	index := make(map[string]reflect.Value)
	rt := root.Type()
	for i := 0; i < rt.NumField(); i++ {
		sf := rt.Field(i)
		if sf.PkgPath != "" { // 未导出字段
			continue
		}
		name := sf.Name
		if tag, ok := sf.Tag.Lookup("json"); ok {
			parts := strings.Split(tag, ",")
			if parts[0] == "-" {
				continue
			}
			if parts[0] != "" {
				name = parts[0]
			}
		}
		index[name] = root.Field(i)
	}
	return index, nil
}

// decodeObject 把请求体解码为键到原始值的映射，拒绝非对象与尾随数据。
func decodeObject(payload []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	var obj map[string]json.RawMessage
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf("binder: invalid JSON object: %w", err)
	}
	if obj == nil {
		return nil, errors.New("binder: payload must be a JSON object")
	}
	if dec.More() {
		return nil, errors.New("binder: trailing data after JSON object")
	}
	return obj, nil
}

// BindClient 按白名单把客户端请求体绑定到 dst。
// 只复制白名单内、非 null 且类型正确的值；其余全部拒绝并记录。
// 绑定本身不因拒绝而报错，调用方通过 Result.Rejected 审计。
func (s *Schema) BindClient(payload []byte, dst any) (*Result, error) {
	obj, err := decodeObject(payload)
	if err != nil {
		return nil, err
	}
	index, err := fieldIndex(dst)
	if err != nil {
		return nil, err
	}
	writable := s.writableSet()
	res := &Result{}

	for key, raw := range obj {
		if !writable[key] {
			reason := s.classify(key)
			res.Rejected = append(res.Rejected, Rejection{Field: key, Reason: reason})
			s.logger.Warn("binder: client field rejected",
				"schema", s.Name, "field", key, "reason", reason)
			continue
		}
		fv, ok := index[key]
		if !ok {
			res.Rejected = append(res.Rejected, Rejection{Field: key, Reason: ReasonUnknownField})
			s.logger.Warn("binder: whitelisted field missing on target",
				"schema", s.Name, "field", key)
			continue
		}
		if err := assignValue(fv, raw); err != nil {
			reason := ReasonTypeMismatch
			if errors.Is(err, errNull) {
				reason = ReasonNullValue
			} else if errors.Is(err, errNested) {
				reason = ReasonNestedObject
			}
			res.Rejected = append(res.Rejected, Rejection{Field: key, Reason: reason})
			s.logger.Warn("binder: client field rejected",
				"schema", s.Name, "field", key, "reason", reason)
			continue
		}
		res.Applied = append(res.Applied, key)
	}
	return res, nil
}

var (
	errNull   = errors.New("null value")
	errNested = errors.New("nested object or array")
)

// assignValue 把单个 JSON 值赋给结构体字段，带严格类型检查。
// null 与针对标量字段的嵌套对象/数组一律拒绝，不做部分写入。
func assignValue(fv reflect.Value, raw json.RawMessage) error {
	if !fv.CanSet() {
		return errors.New("field not settable")
	}
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return errNull
	}
	isComposite := len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
	kind := fv.Kind()
	isCompositeKind := kind == reflect.Struct || kind == reflect.Map ||
		kind == reflect.Slice || kind == reflect.Array
	if isComposite && !isCompositeKind {
		return errNested
	}
	fresh := reflect.New(fv.Type())
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(fresh.Interface()); err != nil {
		if isComposite {
			return fmt.Errorf("%w: %v", errNested, err)
		}
		return err
	}
	fv.Set(fresh.Elem())
	return nil
}
