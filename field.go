package typepipe

// Kind 是管道输出的规范化目标类型。
type Kind string

const (
	KindBool      Kind = "bool"
	KindInt       Kind = "int"
	KindFloat     Kind = "float"
	KindString    Kind = "string"
	KindTimestamp Kind = "timestamp" // 带 UTC 偏移的 ISO 8601 字符串
	KindUUID      Kind = "uuid"      // 规范化小写连字符形式
	KindJSON      Kind = "json"
)

// Field 描述一个源列到目标类型的映射规则。
type Field struct {
	Name       string           // 字段名
	SourceDB   string           // mysql / postgres / jsondoc
	SourceType string           // 原始类型声明，如 datetime、enum('a','b')
	Kind       Kind             // 目标规范化类型
	Converter  string           // 转换器名，见 converters 注册表
	EnumMap    map[string]int64 // 枚举值 -> 字典码；为空时从 SourceType 解析
	TZOffset   string           // 朴素时间字面量采用的源时区偏移，如 "+08:00"，默认 UTC
	Min        *int64           // 目标整数下界（含），越界触发溢出处理
	Max        *int64           // 目标整数上界（含）
	Default    any              // 坏字段安全默认值；nil 时按 Kind 取内置默认
	HasDefault bool             // 是否显式指定了 Default
}

// Anomaly 是一条数据清洗异常日志。
type Anomaly struct {
	Field  string `json:"field"`  // 字段名
	Reason string `json:"reason"` // overflow / invalid_format / unknown_enum / null_value / type_mismatch / out_of_domain
	Input  any    `json:"input"`  // 原始输入值
	Action string `json:"action"` // clamped / default_applied
	Output any    `json:"output"` // 实际写入的值
}

func kindDefault(k Kind) any {
	switch k {
	case KindBool:
		return false
	case KindInt:
		return int64(0)
	case KindFloat:
		return float64(0)
	case KindString:
		return ""
	case KindTimestamp:
		return "1970-01-01T00:00:00Z"
	case KindUUID:
		return "00000000-0000-0000-0000-000000000000"
	default:
		return nil
	}
}

func (f *Field) safeDefault() any {
	if f.HasDefault {
		return f.Default
	}
	return kindDefault(f.Kind)
}

func (f *Field) fail(input any, reason string) (any, *Anomaly) {
	out := f.safeDefault()
	return out, &Anomaly{Field: f.Name, Reason: reason, Input: input, Action: "default_applied", Output: out}
}
