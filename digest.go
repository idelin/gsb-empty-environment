package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func canonicalJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("!%#v", value)
	}
	return string(data)
}

func digestString(parts ...string) string {
	hash := sha256.New()
	hash.Write([]byte("netsec-graphql-auth-plan/v1\x00"))
	for _, part := range parts {
		hash.Write([]byte(strconv.Itoa(len(part))))
		hash.Write([]byte(":"))
		hash.Write([]byte(part))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func canonicalValue(value *Value) string {
	if value == nil {
		return "null"
	}
	switch value.Kind {
	case ValueVariable:
		return "$" + value.Variable
	case ValueInt:
		return "int:" + value.Raw
	case ValueFloat:
		return "float:" + value.Raw
	case ValueString:
		return "string:" + strconv.Quote(value.Raw)
	case ValueBoolean:
		return "bool:" + strconv.FormatBool(value.Bool)
	case ValueNull:
		return "null"
	case ValueEnum:
		return "enum:" + value.Raw
	case ValueList:
		var builder strings.Builder
		builder.WriteString("list[")
		for _, item := range value.List {
			builder.WriteString(canonicalValue(item))
			builder.WriteByte(',')
		}
		builder.WriteString("]")
		return builder.String()
	case ValueObject:
		fields := append([]*ObjectValueField(nil), value.Object...)
		sort.SliceStable(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
		var builder strings.Builder
		builder.WriteString("object{")
		for _, field := range fields {
			builder.WriteString(field.Name)
			builder.WriteByte(':')
			builder.WriteString(canonicalValue(field.Value))
			builder.WriteByte(',')
		}
		builder.WriteString("}")
		return builder.String()
	default:
		return fmt.Sprintf("unknown:%d", value.Kind)
	}
}

func canonicalArguments(arguments []*Argument) string {
	if len(arguments) == 0 {
		return "()"
	}
	ordered := append([]*Argument(nil), arguments...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	var builder strings.Builder
	builder.WriteByte('(')
	for _, argument := range ordered {
		builder.WriteString(argument.Name)
		builder.WriteByte(':')
		builder.WriteString(canonicalValue(argument.Value))
		builder.WriteByte(',')
	}
	builder.WriteByte(')')
	return builder.String()
}

func canonicalConditions(conditions []PlannedDirective) string {
	var builder strings.Builder
	for _, condition := range conditions {
		builder.WriteString("@")
		builder.WriteString(condition.Name)
		builder.WriteString("|")
		builder.WriteString(condition.Origin)
		builder.WriteString("|")
		builder.WriteString(canonicalArguments(condition.Arguments))
		builder.WriteByte(';')
	}
	return builder.String()
}

func canonicalStaticPlan(plan *StaticPlan) string {
	if plan == nil || plan.Operation == nil {
		return ""
	}
	operation := plan.Operation
	var builder strings.Builder
	builder.WriteString("operation|")
	builder.WriteString(string(operation.Type))
	builder.WriteByte('|')
	builder.WriteString(operation.Name)
	builder.WriteByte('\n')
	definitions := append([]*VariableDefinition(nil), operation.VariableDefinitions...)
	sort.SliceStable(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	for _, definition := range definitions {
		builder.WriteString("variable|")
		builder.WriteString(definition.Name)
		builder.WriteByte('|')
		builder.WriteString(definition.Type.String())
		builder.WriteByte('|')
		if definition.HasDefault {
			builder.WriteString(canonicalValue(definition.Default))
		} else {
			builder.WriteString("<required>")
		}
		builder.WriteByte('\n')
		for _, directive := range definition.Directives {
			builder.WriteString("variable-directive|")
			builder.WriteString(directive.Name)
			builder.WriteString(canonicalArguments(directive.Arguments))
			builder.WriteByte('\n')
		}
	}
	for _, directive := range operation.Directives {
		builder.WriteString("operation-directive|")
		builder.WriteString(directive.Name)
		builder.WriteString(canonicalArguments(directive.Arguments))
		builder.WriteByte('\n')
	}
	for _, node := range plan.Nodes {
		builder.WriteString("field|")
		builder.WriteString(node.ParentType)
		builder.WriteByte('|')
		builder.WriteString(node.FieldName)
		builder.WriteByte('|')
		builder.WriteString(node.Alias)
		builder.WriteByte('|')
		builder.WriteString(node.Path.Key())
		builder.WriteByte('|')
		builder.WriteString(canonicalArguments(node.Arguments))
		builder.WriteByte('|')
		builder.WriteString(canonicalConditions(node.Conditions))
		builder.WriteByte('|')
		builder.WriteString(strconv.Itoa(node.Depth))
		builder.WriteByte('\n')
	}
	return builder.String()
}

// PlanDigest returns the stable digest of the query shape for an operation. It
// excludes identity, runtime variables, batch IDs, persisted-query IDs, and
// comments/whitespace.
func PlanDigest(query string, operationName ...string) (string, error) {
	plan, err := Compile(query, operationName...)
	if err != nil {
		return "", err
	}
	return plan.Digest, nil
}

// DigestForQuery is an alias for PlanDigest.
func DigestForQuery(query string, operationName ...string) (string, error) {
	return PlanDigest(query, operationName...)
}
