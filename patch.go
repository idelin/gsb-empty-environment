package scim

import (
	"fmt"
	"strings"
)

// PatchRequest 是 RFC 7644 §3.5.2 的 PATCH 请求信封。
type PatchRequest struct {
	Schemas    []string         `json:"schemas,omitempty"`
	Operations []PatchOperation `json:"Operations"`
}

// PatchOperation 是单个 add/remove/replace 操作。
type PatchOperation struct {
	Op    string `json:"op"`
	Path  string `json:"path,omitempty"`
	Value any    `json:"value,omitempty"`
}

// PatchPath 是解析后的 PATCH path：
// attr、attr[filter] 或 attr[filter].subAttr 以及普通点分路径。
type PatchPath struct {
	Raw     string
	Attr    AttrPath
	Filter  Expr
	SubPath []string
}

// IsRoot 报告 path 是否为空（针对整个资源）。
func (p *PatchPath) IsRoot() bool {
	return p == nil || p.Raw == "" || len(p.Attr.Parts) == 0
}

// MutationPath 返回用于 schema / 策略判定的完整属性路径。
func (p *PatchPath) MutationPath() AttrPath {
	if len(p.SubPath) == 0 {
		return p.Attr
	}
	parts := make([]string, 0, len(p.Attr.Parts)+len(p.SubPath))
	parts = append(parts, p.Attr.Parts...)
	parts = append(parts, p.SubPath...)
	return AttrPath{Schema: p.Attr.Schema, Parts: parts}
}

// ParsePatchPath 解析 PATCH path 表达式，方括号内复用 filter 递归下降解析器。
func ParsePatchPath(input string, opts ...Option) (*PatchPath, error) {
	cfg := defaultParseConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if len(input) > cfg.maxInputLength {
		return nil, fmt.Errorf("%w: path length %d exceeds limit %d",
			ErrFilterComplexity, len(input), cfg.maxInputLength)
	}
	if strings.TrimSpace(input) == "" {
		return &PatchPath{Raw: ""}, nil
	}

	toks, err := lexSCIM(input)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPatchPath, err)
	}
	p := &parser{toks: toks, maxDepth: cfg.maxDepth, sentinel: ErrInvalidPatchPath}

	pp := &PatchPath{Raw: input}
	attrPath, err := p.parseAttrPath()
	if err != nil {
		return nil, err
	}
	pp.Attr = attrPath

	if p.cur().kind == tokLBracket {
		p.advance()
		filter, err := p.parseOr(1)
		if err != nil {
			return nil, err
		}
		if t := p.cur(); t.kind != tokRBracket {
			return nil, p.errorf(t.pos, "expected ']' but found %s %q", t.kind, t.lit)
		}
		p.advance()
		pp.Filter = filter
		if d := filter.Depth(); d > cfg.maxDepth {
			return nil, fmt.Errorf("%w: PATCH path filter depth %d exceeds limit %d",
				ErrFilterDepth, d, cfg.maxDepth)
		}
	}

	// 括号过滤器之后的 .subAttr，例如 members[value eq "x"].displayName。
	for p.cur().kind == tokDot {
		p.advance()
		nameTok := p.cur()
		if nameTok.kind != tokIdent {
			return nil, p.errorf(nameTok.pos, "expected sub-attribute name after '.'")
		}
		for _, seg := range strings.Split(nameTok.lit, ".") {
			if !validAttrName(seg) {
				return nil, p.errorf(nameTok.pos, "invalid sub-attribute name %q", seg)
			}
			pp.SubPath = append(pp.SubPath, seg)
		}
		p.advance()
	}

	if t := p.cur(); t.kind != tokEOF {
		return nil, p.errorf(t.pos, "unexpected %s %q in PATCH path", t.kind, t.lit)
	}
	return pp, nil
}

// extractMemberRefs 从 PATCH value 中提取 member.value / member.$ref 引用。
func extractMemberRefs(v any) []string {
	var refs []string
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if t != "" {
			refs = append(refs, t)
		}
	case Resource:
		if ref := memberRef(t); ref != "" {
			refs = append(refs, ref)
		}
	case map[string]any:
		if ref := memberRef(Resource(t)); ref != "" {
			refs = append(refs, ref)
		}
	case []any:
		for _, item := range t {
			refs = append(refs, extractMemberRefs(item)...)
		}
	case []map[string]any:
		for _, item := range t {
			refs = append(refs, extractMemberRefs(item)...)
		}
	}
	return refs
}

func memberRef(m Resource) string {
	if v, ok := m["value"].(string); ok && v != "" {
		return v
	}
	if v, ok := m["$ref"].(string); ok && v != "" {
		return v
	}
	return ""
}

// applyPatchOperation 按已校验的路径把操作应用到资源上。
func applyPatchOperation(r Resource, op PatchOperation, pp *PatchPath) error {
	operation := strings.ToLower(strings.TrimSpace(op.Op))

	if pp.IsRoot() {
		if operation == "remove" {
			return fmt.Errorf("cannot remove entire resource via empty PATCH path")
		}
		values, ok := op.Value.(map[string]any)
		if !ok {
			return fmt.Errorf("PATCH value for empty path must be a JSON object")
		}
		for key, val := range values {
			ap, err := ParseAttrPath(key)
			if err != nil {
				return err
			}
			setDeep(r, ap.Parts, val, operation == "add")
		}
		return nil
	}

	if pp.Filter != nil {
		return applyFiltered(r, pp, operation, op.Value)
	}

	switch operation {
	case "remove":
		removeDeep(r, pp.Attr.Parts)
		return nil
	case "add", "replace":
		setDeep(r, pp.MutationPath().Parts, op.Value, operation == "add")
		return nil
	default:
		return fmt.Errorf("unsupported PATCH operation %q", op.Op)
	}
}

func applyFiltered(r Resource, pp *PatchPath, operation string, value any) error {
	rootKey := pp.Attr.Root()
	actualKey := resolveKeyCI(r, rootKey)
	raw, ok := r[actualKey]
	if !ok {
		// 过滤目标不存在：add 场景创建空数组，其余视为无命中。
		if operation == "add" && len(pp.SubPath) == 0 {
			r[rootKey] = normalizeElements(value)
		}
		return nil
	}
	slice, isSlice := asSlice(raw)
	if !isSlice {
		m, isMap := asMap(raw)
		if !isMap {
			return nil
		}
		slice = []any{m}
	}

	matched := map[int]Resource{}
	for i, el := range slice {
		m, ok := asMap(el)
		if !ok {
			continue
		}
		if ok, _ := Match(pp.Filter, m); ok {
			matched[i] = m
		}
	}

	switch operation {
	case "remove":
		if len(pp.SubPath) > 0 {
			for _, m := range matched {
				removeDeep(m, pp.SubPath)
			}
			return nil
		}
		kept := make([]any, 0, len(slice))
		for i, el := range slice {
			if _, dead := matched[i]; dead {
				continue
			}
			kept = append(kept, el)
		}
		r[actualKey] = kept
		return nil
	case "add", "replace":
		if len(pp.SubPath) > 0 {
			for _, m := range matched {
				setDeep(m, pp.SubPath, value, operation == "add")
			}
			return nil
		}
		for _, m := range matched {
			if vm, ok := value.(map[string]any); ok {
				for k, v := range vm {
					if operation == "add" {
						setDeep(m, []string{k}, v, true)
					} else {
						m[k] = v
					}
				}
				continue
			}
			// 非对象值无法就地合并到复杂多值元素，直接替换命中元素。
			for i := range matched {
				slice[i] = value
			}
		}
		// 若没有任何命中且为 add，按 RFC 追加新值。
		if len(matched) == 0 && operation == "add" {
			r[actualKey] = append(slice, normalizeElements(value)...)
		}
		return nil
	default:
		return fmt.Errorf("unsupported PATCH operation %q", operation)
	}
}

func normalizeElements(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	if s, ok := v.([]map[string]any); ok {
		out := make([]any, len(s))
		for i, m := range s {
			out[i] = m
		}
		return out
	}
	if v == nil {
		return nil
	}
	return []any{v}
}

// resolveKeyCI 返回 map 中与 key 实际匹配的键名，找不到则返回原 key。
func resolveKeyCI(m Resource, key string) string {
	if _, ok := m[key]; ok {
		return key
	}
	for k := range m {
		if strings.EqualFold(k, key) {
			return k
		}
	}
	return key
}

func setDeep(node Resource, parts []string, value any, additive bool) {
	if len(parts) == 0 {
		return
	}
	key := resolveKeyCI(node, parts[0])
	if len(parts) == 1 {
		if additive {
			if existing, ok := node[key]; ok {
				if slice, ok := asSlice(existing); ok {
					node[key] = append(slice, normalizeElements(value)...)
					return
				}
				if em, ok := asMap(existing); ok {
					if vm, ok := value.(map[string]any); ok {
						for k, v := range vm {
							setDeep(em, []string{k}, v, true)
						}
						return
					}
				}
			}
		}
		node[key] = value
		return
	}

	child, ok := node[key]
	if !ok {
		nm := Resource{}
		node[key] = nm
		setDeep(nm, parts[1:], value, additive)
		return
	}
	if slice, ok := asSlice(child); ok {
		for _, el := range slice {
			if m, ok := asMap(el); ok {
				setDeep(m, parts[1:], value, additive)
			}
		}
		return
	}
	cm, ok := asMap(child)
	if !ok {
		cm = Resource{}
		node[key] = cm
	}
	setDeep(cm, parts[1:], value, additive)
}

func removeDeep(node Resource, parts []string) {
	if len(parts) == 0 || node == nil {
		return
	}
	key := resolveKeyCI(node, parts[0])
	if len(parts) == 1 {
		delete(node, key)
		return
	}
	child, ok := node[key]
	if !ok {
		return
	}
	if slice, ok := asSlice(child); ok {
		for _, el := range slice {
			if m, ok := asMap(el); ok {
				removeDeep(m, parts[1:])
			}
		}
		return
	}
	if m, ok := asMap(child); ok {
		removeDeep(m, parts[1:])
	}
}
