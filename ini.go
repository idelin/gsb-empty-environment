// Package netsec 提供一个可安全处理不可信输入的 INI 写入器与解析器。
//
// 写入侧通过字符校验与双引号转义，保证用户输入无法伪造节头或键值行；
// 解析侧负责还原转义，并保证行尾反斜杠续行不会跨越节头。
package netsec

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// KeyValue 是节中的一组键值对，按出现顺序保存。
type KeyValue struct {
	Key   string
	Value string
}

// Section 是一个具名 INI 节，Name 为空表示节头之前的全局节。
type Section struct {
	Name  string
	Pairs []KeyValue
}

// Config 是按文件顺序保存的 INI 文档，允许重名节与重复键。
type Config struct {
	Sections []Section
}

// NewConfig 创建一个仅含空全局节的配置。
func NewConfig() *Config {
	return &Config{Sections: []Section{{Name: ""}}}
}

// Set 校验并写入键值对；节或键已存在时更新值且保持原有顺序，否则追加。
func (c *Config) Set(section, key, value string) error {
	if err := ValidateSection(section); err != nil {
		return err
	}
	if err := ValidateKey(key); err != nil {
		return err
	}
	if err := ValidateValue(value); err != nil {
		return err
	}
	for i := range c.Sections {
		if c.Sections[i].Name != section {
			continue
		}
		for j, kv := range c.Sections[i].Pairs {
			if kv.Key == key {
				c.Sections[i].Pairs[j].Value = value
				return nil
			}
		}
		c.Sections[i].Pairs = append(c.Sections[i].Pairs, KeyValue{Key: key, Value: value})
		return nil
	}
	c.Sections = append(c.Sections, Section{
		Name:  section,
		Pairs: []KeyValue{{Key: key, Value: value}},
	})
	return nil
}

// Get 返回最近一次写入的同名键值；键不存在时 ok 为 false。
func (c *Config) Get(section, key string) (value string, ok bool) {
	for i := len(c.Sections) - 1; i >= 0; i-- {
		if c.Sections[i].Name != section {
			continue
		}
		for j := len(c.Sections[i].Pairs) - 1; j >= 0; j-- {
			if c.Sections[i].Pairs[j].Key == key {
				return c.Sections[i].Pairs[j].Value, true
			}
		}
	}
	return "", false
}

// ValidateKey 拒绝空键、含 = : ; # 换行/NUL 的键、首尾为空白的键，
// 以及以 [ 开头的键（否则写出的行会被读成节头，无法满足往返一致）。
func ValidateKey(key string) error {
	if key == "" {
		return errors.New("ini: key must not be empty")
	}
	if i := strings.IndexAny(key, "=:;#\n\r\x00"); i >= 0 {
		return fmt.Errorf("ini: key %q contains illegal character %q", key, key[i])
	}
	if key[0] == '[' {
		return fmt.Errorf("ini: key %q must not begin with '['", key)
	}
	if hasEdgeWhitespace(key) {
		return fmt.Errorf("ini: key %q must not have leading or trailing whitespace", key)
	}
	return nil
}

// ValidateSection 拒绝含 ]、换行或 NUL 的节名；空节名（全局节）合法。
func ValidateSection(name string) error {
	if i := strings.IndexAny(name, "]\n\r\x00"); i >= 0 {
		return fmt.Errorf("ini: section name %q contains illegal character %q", name, name[i])
	}
	return nil
}

// ValidateValue 拒绝含 NUL 的值；换行与 CR 允许存在，但必须经引号转义写入。
func ValidateValue(value string) error {
	if strings.ContainsRune(value, '\x00') {
		return errors.New("ini: value must not contain NUL")
	}
	return nil
}

func hasEdgeWhitespace(s string) bool {
	if s == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(s)
	if unicode.IsSpace(first) {
		return true
	}
	last, _ := utf8.DecodeLastRuneInString(s)
	return unicode.IsSpace(last)
}

// needsQuotes 判断值是否必须以双引号包裹，避免被读成节头、注释或丢失空白/转义。
func needsQuotes(value string) bool {
	if value == "" {
		return false
	}
	// 引号与反斜杠本身也需要引号包裹，否则解析端无法原样还原。
	if strings.ContainsAny(value, "\n\r\"\\") {
		return true
	}
	switch value[0] {
	case '[', ';', '#':
		return true
	}
	return hasEdgeWhitespace(value)
}

func quoteValue(value string) string {
	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Marshal 将配置序列化为 INI 文本；全局节始终写在最前。
// 任何未通过校验的节名、键或值都会返回错误而不是落盘。
func Marshal(c *Config) ([]byte, error) {
	var b strings.Builder
	writeSection := func(s *Section) error {
		if err := ValidateSection(s.Name); err != nil {
			return err
		}
		if s.Name != "" {
			b.WriteByte('[')
			b.WriteString(s.Name)
			b.WriteString("]\n")
		}
		for _, kv := range s.Pairs {
			if err := ValidateKey(kv.Key); err != nil {
				return err
			}
			if err := ValidateValue(kv.Value); err != nil {
				return err
			}
			b.WriteString(kv.Key)
			b.WriteString(" = ")
			if needsQuotes(kv.Value) {
				b.WriteString(quoteValue(kv.Value))
			} else {
				b.WriteString(kv.Value)
			}
			b.WriteByte('\n')
		}
		return nil
	}

	for i := range c.Sections {
		if c.Sections[i].Name == "" {
			if err := writeSection(&c.Sections[i]); err != nil {
				return nil, err
			}
			break
		}
	}
	for i := range c.Sections {
		if c.Sections[i].Name != "" {
			if err := writeSection(&c.Sections[i]); err != nil {
				return nil, err
			}
		}
	}
	return []byte(b.String()), nil
}

type logicalLine struct {
	text   string
	lineNo int
}

// joinPhysicalLines 合并行尾反斜杠续行；遇到节头行时立即结束续行，
// 节头永远不会被并入上一个值，未被消费的反斜杠按普通字符保留。
func joinPhysicalLines(data []byte) []logicalLine {
	lines := strings.Split(string(data), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	physical := make([]string, 0, len(lines))
	for _, line := range lines {
		physical = append(physical, strings.TrimSuffix(line, "\r"))
	}

	var logical []logicalLine
	pending := ""
	pendingLine := 0
	for i, line := range physical {
		if pending != "" {
			if isSectionHeaderLine(line) {
				logical = append(logical, logicalLine{text: pending, lineNo: pendingLine})
				pending = ""
			} else {
				// 真正续行：消费上一行末尾的一个反斜杠后直接拼接。
				pending = pending[:len(pending)-1] + line
				if !strings.HasSuffix(line, "\\") {
					logical = append(logical, logicalLine{text: pending, lineNo: pendingLine})
					pending = ""
				}
				continue
			}
		}
		if strings.HasSuffix(line, "\\") {
			pending = line
			pendingLine = i + 1
		} else {
			logical = append(logical, logicalLine{text: line, lineNo: i + 1})
		}
	}
	if pending != "" {
		logical = append(logical, logicalLine{text: pending, lineNo: pendingLine})
	}
	return logical
}

func isSectionHeaderLine(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), "[")
}

// Unmarshal 解析 INI 文本，还原引号转义，并重建节与键的原始顺序。
func Unmarshal(data []byte) (*Config, error) {
	cfg := NewConfig()
	current := 0

	for _, ll := range joinPhysicalLines(data) {
		line := strings.TrimLeft(ll.text, " \t")
		if line == "" || line[0] == ';' || line[0] == '#' {
			continue
		}
		if line[0] == '[' {
			closeIdx := strings.IndexByte(line, ']')
			if closeIdx < 0 {
				return nil, fmt.Errorf("ini: unterminated section header at line %d", ll.lineNo)
			}
			name := line[1:closeIdx]
			tail := strings.TrimLeft(line[closeIdx+1:], " \t")
			if tail != "" && tail[0] != ';' && tail[0] != '#' {
				return nil, fmt.Errorf("ini: unexpected content after section header at line %d", ll.lineNo)
			}
			if err := ValidateSection(name); err != nil {
				return nil, err
			}
			cfg.Sections = append(cfg.Sections, Section{Name: name})
			current = len(cfg.Sections) - 1
			continue
		}

		sep := strings.IndexAny(line, "=:")
		if sep < 0 {
			return nil, fmt.Errorf("ini: missing key/value separator at line %d", ll.lineNo)
		}
		key := strings.TrimRight(line[:sep], " \t")
		value, err := parseFieldValue(line[sep+1:], ll.lineNo)
		if err != nil {
			return nil, err
		}
		cfg.Sections[current].Pairs = append(cfg.Sections[current].Pairs, KeyValue{
			Key:   key,
			Value: value,
		})
	}
	return cfg, nil
}

func parseFieldValue(raw string, lineNo int) (string, error) {
	raw = strings.TrimLeft(raw, " \t")
	if raw == "" || raw[0] != '"' {
		return strings.TrimRight(raw, " \t"), nil
	}

	var b strings.Builder
	for i := 1; i < len(raw); {
		switch raw[i] {
		case '"':
			tail := strings.TrimLeft(raw[i+1:], " \t")
			if tail != "" && tail[0] != ';' && tail[0] != '#' {
				return "", fmt.Errorf("ini: unexpected content after closing quote at line %d", lineNo)
			}
			return b.String(), nil
		case '\\':
			if i+1 >= len(raw) {
				return "", fmt.Errorf("ini: dangling escape sequence at line %d", lineNo)
			}
			switch raw[i+1] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			default:
				// 未知转义保留反斜杠与后续字符的字面形式。
				b.WriteByte('\\')
				b.WriteByte(raw[i+1])
			}
			i += 2
		default:
			b.WriteByte(raw[i])
			i++
		}
	}
	return "", fmt.Errorf("ini: unterminated quoted value at line %d", lineNo)
}
