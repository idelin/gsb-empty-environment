package typepipe

import (
	"fmt"
	"strings"
)

// Column 描述源库中的一列。
type Column struct {
	Name string
	Type string // 源类型声明，如 "tinyint(1)"、"enum('a','b')"、"timestamp with time zone"
}

// Options 调整预设映射行为。
type Options struct {
	TZOffset string // 朴素日期时间字面量的源时区偏移，默认 "Z"（UTC）
}

func i64(v int64) *int64 { return &v }

var intRanges = map[string][2]int64{
	"tinyint":   {-128, 127},
	"smallint":  {-32768, 32767},
	"mediumint": {-8388608, 8388607},
	"int":       {-2147483648, 2147483647},
}

// NewPipelineFromSchema 按预设映射规则，从源库表结构直接构建管道。
// 支持 mysql、postgres、jsondoc 三种来源。
func NewPipelineFromSchema(sourceDB string, cols []Column, opts Options) (*Pipeline, error) {
	fields := make([]*Field, 0, len(cols))
	for _, c := range cols {
		f, err := mapColumn(sourceDB, c, opts)
		if err != nil {
			return nil, err
		}
		fields = append(fields, f)
	}
	return NewPipeline(fields...)
}

func mapColumn(sourceDB string, c Column, opts Options) (*Field, error) {
	f := &Field{Name: c.Name, SourceDB: sourceDB, SourceType: c.Type, TZOffset: opts.TZOffset}
	t := strings.ToLower(strings.TrimSpace(c.Type))
	unsigned := strings.Contains(t, "unsigned")
	base := t
	if i := strings.IndexAny(base, "( "); i >= 0 {
		base = base[:i]
	}

	switch sourceDB {
	case "mysql":
		switch {
		case t == "tinyint(1)":
			f.Kind, f.Converter = KindBool, "bool"
		case base == "tinyint" || base == "smallint" || base == "mediumint" || base == "int" || base == "integer":
			if base == "integer" {
				base = "int"
			}
			r := intRanges[base]
			f.Min, f.Max = i64(r[0]), i64(r[1])
			if unsigned {
				f.Min, f.Max = i64(0), i64(r[1]*2+1)
			}
			f.Kind, f.Converter = KindInt, "int"
		case base == "bigint":
			if unsigned {
				f.Min = i64(0)
			}
			f.Kind, f.Converter = KindInt, "int"
		case base == "datetime" || base == "timestamp":
			f.Kind, f.Converter = KindTimestamp, "datetime"
		case strings.HasPrefix(t, "enum"):
			f.Kind, f.Converter = KindInt, "enum"
		case t == "char(36)" || t == "uuid":
			f.Kind, f.Converter = KindUUID, "uuid"
		case base == "char" || base == "varchar" || strings.HasSuffix(base, "text") || base == "set":
			f.Kind, f.Converter = KindString, "string"
		case base == "decimal" || base == "numeric" || base == "float" || base == "double" || base == "real":
			f.Kind, f.Converter = KindFloat, "float"
		case base == "json":
			f.Kind, f.Converter = KindJSON, "json"
		default:
			return nil, fmt.Errorf("typepipe: no preset rule for mysql type %q", c.Type)
		}
	case "postgres":
		switch {
		case base == "boolean" || base == "bool":
			f.Kind, f.Converter = KindBool, "bool"
		case base == "smallint" || base == "int2":
			f.Min, f.Max = i64(-32768), i64(32767)
			f.Kind, f.Converter = KindInt, "int"
		case base == "integer" || base == "int" || base == "int4":
			f.Min, f.Max = i64(-2147483648), i64(2147483647)
			f.Kind, f.Converter = KindInt, "int"
		case base == "bigint" || base == "int8":
			f.Kind, f.Converter = KindInt, "int"
		case base == "uuid":
			f.Kind, f.Converter = KindUUID, "uuid"
		case strings.HasPrefix(t, "timestamp") || strings.HasPrefix(t, "date") || strings.HasPrefix(t, "time"):
			f.Kind, f.Converter = KindTimestamp, "datetime"
		case base == "text" || base == "varchar" || base == "character" || base == "char" || base == "name":
			f.Kind, f.Converter = KindString, "string"
		case base == "numeric" || base == "decimal" || base == "real" || base == "float4" || base == "float8" || t == "double precision":
			f.Kind, f.Converter = KindFloat, "float"
		case base == "json" || base == "jsonb":
			f.Kind, f.Converter = KindJSON, "json"
		default:
			return nil, fmt.Errorf("typepipe: no preset rule for postgres type %q", c.Type)
		}
	case "jsondoc":
		switch base {
		case "bool", "boolean":
			f.Kind, f.Converter = KindBool, "bool"
		case "int", "integer", "long":
			f.Kind, f.Converter = KindInt, "int"
		case "number", "float", "double":
			f.Kind, f.Converter = KindFloat, "float"
		case "string":
			f.Kind, f.Converter = KindString, "string"
		case "date", "datetime", "timestamp":
			f.Kind, f.Converter = KindTimestamp, "datetime"
		case "uuid":
			f.Kind, f.Converter = KindUUID, "uuid"
		case "object", "array", "json":
			f.Kind, f.Converter = KindJSON, "json"
		default:
			return nil, fmt.Errorf("typepipe: no preset rule for jsondoc type %q", c.Type)
		}
	default:
		return nil, fmt.Errorf("typepipe: unsupported source db %q", sourceDB)
	}
	return f, nil
}
