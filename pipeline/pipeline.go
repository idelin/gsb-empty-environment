// Package pipeline implements a heterogeneous data type conversion and
// format mapping engine across MySQL, PostgreSQL and JSON document stores.
//
// The engine is driven by preset mapping rules. Fields that cannot be
// converted cleanly fall back to safe defaults and are reported as data
// cleaning issues (both collected in memory and written to a log sink).
package pipeline

import (
	"fmt"
	"io"
	"log"
	"strings"
	"time"
)

// SourceDB identifies the database family a record originates from.
type SourceDB string

const (
	MySQL    SourceDB = "mysql"
	Postgres SourceDB = "postgres"
	JSONDoc  SourceDB = "jsondoc"
)

// Canonical target types produced by the pipeline.
const (
	TypeBool     = "bool"     // e.g. MySQL tinyint(1) -> boolean
	TypeISO8601  = "iso8601"  // timestamp rendered as ISO-8601 with UTC offset
	TypeDictCode = "dictcode" // enum label -> integer dictionary code
	TypeUUID     = "uuid"     // canonical lowercase UUID string
	TypeString   = "string"   // e.g. PostgreSQL uuid -> plain string
	TypeInt      = "int"      // bounded integer with overflow handling
)

// FieldType describes a raw source column type, e.g. enum('a','b') or
// tinyint(1).
type FieldType struct {
	Name string   // raw type name: "tinyint", "datetime", "enum", ...
	Args []string // type arguments: display width, enum members, ...
}

func (ft FieldType) String() string {
	if len(ft.Args) == 0 {
		return ft.Name
	}
	return fmt.Sprintf("%s(%s)", ft.Name, strings.Join(ft.Args, ","))
}

// Field pairs a column name with its source type.
type Field struct {
	Name string
	Type FieldType
}

// OverflowPolicy decides how out-of-range numbers are cleaned.
type OverflowPolicy int

const (
	// Clamp pulls out-of-range values to the nearest bound.
	Clamp OverflowPolicy = iota
	// UseDefault replaces out-of-range values with the safe default.
	UseDefault
)

// Rule is a preset mapping from a source column type to a canonical target.
type Rule struct {
	Source   SourceDB       // database family the rule applies to
	Match    string         // source type name to match, e.g. "datetime"
	Target   string         // canonical target type, one of the Type* constants
	Dict     map[string]int // enum label -> dictionary code (TypeDictCode)
	Location *time.Location // assumed zone for naive datetimes (TypeISO8601)
	Min, Max int64          // inclusive bounds (TypeInt)
	Default  any            // explicit safe default; nil picks the type default
	Overflow OverflowPolicy // overflow strategy (TypeInt)
}

// Issue records a single data cleaning event.
type Issue struct {
	Field  string // column name
	Type   string // source column type
	Value  any    // offending input value
	Reason string // why the value could not be converted cleanly
	Effect string // what the pipeline did about it
}

func (is Issue) String() string {
	return fmt.Sprintf("field=%s type=%s value=%v reason=%q effect=%s",
		is.Field, is.Type, is.Value, is.Reason, is.Effect)
}

// Engine converts records according to preset rules and reports issues.
type Engine struct {
	rules  []Rule
	issues []Issue
	logger *log.Logger
}

// New builds an Engine. logOut may be nil to disable the cleaning log.
func New(rules []Rule, logOut io.Writer) *Engine {
	e := &Engine{rules: rules}
	if logOut != nil {
		e.logger = log.New(logOut, "[clean] ", 0)
	}
	return e
}

// Issues returns the cleaning issues collected so far.
func (e *Engine) Issues() []Issue {
	return append([]Issue(nil), e.issues...)
}

func (e *Engine) findRule(src SourceDB, typeName string) (Rule, bool) {
	for _, r := range e.rules {
		if r.Source == src && r.Match == typeName {
			return r, true
		}
	}
	return Rule{}, false
}

// ConvertRecord converts every schema field found in row. Fields without a
// matching rule pass through unchanged.
func (e *Engine) ConvertRecord(src SourceDB, schema []Field, row map[string]any) map[string]any {
	out := make(map[string]any, len(schema))
	for _, f := range schema {
		out[f.Name] = e.Convert(src, f, row[f.Name])
	}
	return out
}

// Convert maps a single field value to its canonical target form.
func (e *Engine) Convert(src SourceDB, f Field, v any) any {
	rule, ok := e.findRule(src, f.Type.Name)
	if !ok {
		return v // no preset rule: pass through untouched
	}
	if v == nil {
		d := defaultFor(rule)
		e.report(Issue{Field: f.Name, Type: f.Type.String(), Value: nil,
			Reason: "missing or null value", Effect: fmt.Sprintf("default %v", d)})
		return d
	}
	out, issue := convert(rule, f, v)
	if issue != nil {
		e.report(*issue)
	}
	return out
}

func (e *Engine) report(is Issue) {
	e.issues = append(e.issues, is)
	if e.logger != nil {
		e.logger.Print(is.String())
	}
}
