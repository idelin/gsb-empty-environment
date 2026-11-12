package taskpkg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
)

// Options is the host-facing input for Build. Scripts may be a slice of
// json.RawMessage, a slice of already decoded JSON trees, or a single JSON
// document. ScriptTrees is an alias for Scripts.
//
// PageURL must be absolute. ArticleTypes and PersonTypes are expanded type
// IRIs; the schema.org HTTPS defaults are used when they are empty.
type Options struct {
	Scripts     any
	ScriptTrees any
	ScriptJSONs any
	JSONTrees   any

	PageURL string
	URL     string
	BaseURL string

	ArticleTypes        []string
	ArticleTypeIRIs     []string
	ArticleIRIs         []string
	AllowedArticleTypes []string
	ArticleType         string
	PersonTypes         []string
	PersonTypeIRIs      []string
	PersonIRIs          []string
	AllowedPersonTypes  []string
	PersonType          string

	SingleValued             any
	SingleValuedProperties   []string
	SingleValuedIRIs         []string
	SingleValuedFields       []string
	SingleValuedPropertyIRIs []string
}

// Aliases make the option type discoverable under the names commonly used by
// extraction hosts.
type Config = Options
type GraphOptions = Options
type BuildOptions = Options
type Input = Options
type GraphInput = Options
type ExtractionInput = Options
type ExtractOptions = Options
type MetadataInput = Options

// ScriptBlock is a convenience input form carrying an explicit source index.
type ScriptBlock struct {
	Index int
	JSON  any
}

type scriptInput struct {
	tree any
	hash string
}

// Builder accumulates script blocks and later produces a merged graph.
type Builder struct {
	pageURL       string
	articleTypes  []string
	personTypes   []string
	single        []string
	scripts       []scriptInput
	unresolvedCtx []UnresolvedContext
	buildErr      error
	currentNodes  map[string]*Node
}

// NewBuilder accepts either NewBuilder(options) or
// NewBuilder(pageURL, articleTypes, personTypes[, singleValued]).
func NewBuilder(args ...any) *Builder {
	b := &Builder{}
	b.applyArgs(args)
	return b
}

// NewGraphBuilder is an alias for NewBuilder.
func NewGraphBuilder(args ...any) *Builder { return NewBuilder(args...) }

// GraphBuilder is an alias for NewBuilder.
func GraphBuilder(args ...any) *Builder { return NewBuilder(args...) }

func (b *Builder) applyArgs(args []any) {
	if len(args) == 1 {
		if o, ok := asOptions(args[0]); ok {
			b.applyOptions(o)
			return
		}
	}
	if len(args) > 0 {
		if urlValue, ok := args[0].(string); ok && !looksLikeJSON(urlValue) {
			b.pageURL = urlValue
			if len(args) > 1 && isTypeList(args[1]) {
				b.articleTypes = append(b.articleTypes, asStrings(args[1])...)
				if len(args) > 2 && isTypeList(args[2]) {
					b.personTypes = append(b.personTypes, asStrings(args[2])...)
				}
				if len(args) > 3 {
					b.single = append(b.single, asStrings(args[3])...)
				}
			} else {
				if len(args) > 1 {
					b.addArgument(args[1])
				}
				if len(args) > 2 {
					b.articleTypes = append(b.articleTypes, asStrings(args[2])...)
				}
				if len(args) > 3 {
					b.personTypes = append(b.personTypes, asStrings(args[3])...)
				}
				if len(args) > 4 {
					b.single = append(b.single, asStrings(args[4])...)
				}
			}
			return
		}
		if scriptValue, ok := args[0].(string); ok && looksLikeJSON(scriptValue) {
			b.addScriptValue(scriptValue, false, -1)
			if len(args) > 1 {
				if urlValue, ok := args[1].(string); ok {
					b.pageURL = urlValue
				}
			}
			if len(args) > 2 {
				b.articleTypes = append(b.articleTypes, asStrings(args[2])...)
			}
			if len(args) > 3 {
				b.personTypes = append(b.personTypes, asStrings(args[3])...)
			}
			if len(args) > 4 {
				b.single = append(b.single, asStrings(args[4])...)
			}
			return
		}
		if isScriptsValue(args[0]) {
			for _, s := range asScripts(args[0]) {
				b.addScriptValue(s, false, -1)
			}
			if len(args) > 1 {
				if urlValue, ok := args[1].(string); ok {
					b.pageURL = urlValue
				}
			}
			if len(args) > 2 {
				b.articleTypes = append(b.articleTypes, asStrings(args[2])...)
			}
			if len(args) > 3 {
				b.personTypes = append(b.personTypes, asStrings(args[3])...)
			}
			if len(args) > 4 {
				b.single = append(b.single, asStrings(args[4])...)
			}
			return
		}
	}
	if len(args) > 0 {
		b.addScriptValue(args[0], false, -1)
	}
}

func (b *Builder) addArgument(value any) {
	if isTypeList(value) {
		return
	}
	for _, item := range asScripts(value) {
		b.addScriptValue(item, false, -1)
	}
}

func isTypeList(value any) bool {
	values := asStrings(value)
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") ||
			strings.HasPrefix(trimmed, "\"") {
			return false
		}
	}
	return true
}

// SetPageURL replaces the document base used for relative @id and @base
// values.
func (b *Builder) SetPageURL(pageURL string) *Builder {
	b.pageURL = pageURL
	return b
}

// SetArticleTypes declares the allowed article type IRIs.
func (b *Builder) SetArticleTypes(types ...string) *Builder {
	b.articleTypes = append([]string(nil), types...)
	return b
}

// SetPersonTypes declares the allowed Person type IRIs.
func (b *Builder) SetPersonTypes(types ...string) *Builder {
	b.personTypes = append([]string(nil), types...)
	return b
}

// SetTypes declares both allowed type sets.
func (b *Builder) SetTypes(articleTypes, personTypes []string) *Builder {
	b.SetArticleTypes(articleTypes...)
	return b.SetPersonTypes(personTypes...)
}

func (b *Builder) applyOptions(o Options) {
	if o.PageURL != "" {
		b.pageURL = o.PageURL
	} else if o.URL != "" {
		b.pageURL = o.URL
	} else if o.BaseURL != "" {
		b.pageURL = o.BaseURL
	}
	scripts := o.Scripts
	if scripts == nil {
		scripts = o.ScriptTrees
	}
	if scripts == nil {
		scripts = o.ScriptJSONs
	}
	if scripts == nil {
		scripts = o.JSONTrees
	}
	if scripts != nil {
		for _, s := range asScripts(scripts) {
			b.addScriptValue(s, false, -1)
		}
	}
	b.articleTypes = append(b.articleTypes, o.ArticleTypes...)
	b.articleTypes = append(b.articleTypes, o.ArticleTypeIRIs...)
	b.articleTypes = append(b.articleTypes, o.ArticleIRIs...)
	b.articleTypes = append(b.articleTypes, o.AllowedArticleTypes...)
	if o.ArticleType != "" {
		b.articleTypes = append(b.articleTypes, o.ArticleType)
	}
	b.personTypes = append(b.personTypes, o.PersonTypes...)
	b.personTypes = append(b.personTypes, o.PersonTypeIRIs...)
	b.personTypes = append(b.personTypes, o.PersonIRIs...)
	b.personTypes = append(b.personTypes, o.AllowedPersonTypes...)
	if o.PersonType != "" {
		b.personTypes = append(b.personTypes, o.PersonType)
	}
	b.single = append(b.single, asStrings(o.SingleValued)...)
	b.single = append(b.single, o.SingleValuedProperties...)
	b.single = append(b.single, o.SingleValuedIRIs...)
	b.single = append(b.single, o.SingleValuedFields...)
	b.single = append(b.single, o.SingleValuedPropertyIRIs...)
}

func asOptions(value any) (Options, bool) {
	switch o := value.(type) {
	case Options:
		return o, true
	case *Options:
		if o != nil {
			return *o, true
		}
	}
	return Options{}, false
}

// SetSingleValued declares additional single-valued property IRIs (or terms
// whose local names identify a single expanded property).
func (b *Builder) SetSingleValued(properties ...string) *Builder {
	b.single = append(b.single, properties...)
	return b
}

// SingleValued is an alias for SetSingleValued.
func (b *Builder) SingleValued(properties ...string) *Builder {
	return b.SetSingleValued(properties...)
}

// AddScript accepts raw JSON ([]byte, string or json.RawMessage) or an already
// decoded JSON tree.
func (b *Builder) AddScript(value any) error {
	return b.addScriptValue(value, false, -1)
}

// AddJSON is an alias for AddScript.
func (b *Builder) AddJSON(value any) error { return b.AddScript(value) }

// AddScriptJSON is an alias for AddScript.
func (b *Builder) AddScriptJSON(value any) error { return b.AddScript(value) }

// AddTree accepts an already decoded JSON tree.
func (b *Builder) AddTree(value any) error {
	return b.addScriptValue(value, false, -1)
}

// AddBlock appends a script block with an optional explicit index. The index is
// informational; merging does not depend on it.
func (b *Builder) AddBlock(block ScriptBlock) error {
	if block.Index < 0 {
		block.Index = -1
	}
	return b.addScriptValue(block.JSON, false, block.Index)
}

func (b *Builder) addScriptValue(value any, _ bool, explicitIndex int) error {
	switch v := value.(type) {
	case ScriptBlock:
		return b.addScriptValue(v.JSON, false, v.Index)
	case *ScriptBlock:
		if v != nil {
			return b.addScriptValue(v.JSON, false, v.Index)
		}
		return fmt.Errorf("nil script block")
	case []json.RawMessage:
		for _, script := range v {
			if err := b.addScriptValue(script, false, -1); err != nil {
				return err
			}
		}
		return nil
	case json.RawMessage:
		tree, err := decodeJSON(v)
		if err != nil {
			b.buildErr = err
			return err
		}
		b.appendScript(tree, v)
		return nil
	case []byte:
		tree, err := decodeJSON(v)
		if err != nil {
			b.buildErr = err
			return err
		}
		b.appendScript(tree, v)
		return nil
	case string:
		if tree, ok := parseJSONText(v); ok {
			b.appendScript(tree, []byte(v))
			return nil
		}
		if looksLikeJSON(v) {
			_, err := decodeJSON([]byte(v))
			if err == nil {
				err = fmt.Errorf("invalid JSON script block")
			}
			b.buildErr = err
			return err
		}
		b.appendScript(v, nil)
		return nil
	case nil:
		return nil
	default:
		b.appendScript(value, nil)
		return nil
	}
}

func (b *Builder) appendScript(tree any, raw []byte) {
	hash := ""
	if len(raw) > 0 {
		if parsed, err := decodeJSON(raw); err == nil {
			hash = sha256Text(canonicalJSON(parsed))
		}
	}
	if hash == "" {
		hash = sha256Text(canonicalJSON(tree))
	}
	b.scripts = append(b.scripts, scriptInput{tree: tree, hash: hash})
}

func decodeJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("multiple JSON values in script block")
	}
	return out, nil
}

func parseJSONText(value string) (any, bool) {
	if !looksLikeJSON(value) {
		return nil, false
	}
	tree, err := decodeJSON([]byte(value))
	if err != nil {
		return nil, false
	}
	return tree, true
}

func looksLikeJSON(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	return trimmed[0] == '{' || trimmed[0] == '[' || trimmed[0] == '"' ||
		trimmed[0] == 't' || trimmed[0] == 'f' || trimmed[0] == 'n' ||
		trimmed[0] == '-' || trimmed[0] >= '0' && trimmed[0] <= '9'
}

func sha256Text(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func isScriptsValue(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		return true
	}
	switch value.(type) {
	case json.RawMessage, []byte:
		return true
	case ScriptBlock, *ScriptBlock:
		return true
	}
	return false
}

func asScripts(value any) []any {
	if value == nil {
		return nil
	}
	switch typed := value.(type) {
	case json.RawMessage, []byte:
		return []any{typed}
	}
	if blocks, ok := value.([]ScriptBlock); ok {
		out := make([]any, 0, len(blocks))
		for _, block := range blocks {
			out = append(out, block)
		}
		return out
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		out := make([]any, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out = append(out, rv.Index(i).Interface())
		}
		return out
	}
	return []any{value}
}

func asStrings(value any) []string {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case string:
		return []string{v}
	case map[string]bool:
		var out []string
		for key, enabled := range v {
			if enabled {
				out = append(out, key)
			}
		}
		sort.Strings(out)
		return out
	case map[string]struct{}:
		var out []string
		for key := range v {
			out = append(out, key)
		}
		sort.Strings(out)
		return out
	case []string:
		return append([]string(nil), v...)
	case json.RawMessage, []byte:
		return nil
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		out := make([]string, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			if s, ok := rv.Index(i).Interface().(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	if rv.Kind() == reflect.Map {
		var out []string
		for _, key := range rv.MapKeys() {
			if key.Kind() != reflect.String {
				continue
			}
			entry := rv.MapIndex(key)
			switch entry.Kind() {
			case reflect.Bool:
				if !entry.Bool() {
					continue
				}
			case reflect.String:
				if entry.String() == "" {
					continue
				}
			}
			out = append(out, key.String())
		}
		sort.Strings(out)
		return out
	}
	return nil
}

// Build accepts Build(options) or positional
// Build(scripts, pageURL, articleTypes, personTypes[, singleValued]).
func Build(args ...any) (*Graph, error) {
	b := NewBuilder(args...)
	return b.Build()
}

// BuildGraph is an alias for Build.
func BuildGraph(args ...any) (*Graph, error) { return Build(args...) }

// Extract is an alias for Build.
func Extract(args ...any) (*Graph, error) { return Build(args...) }

// ExtractGraph is an alias for Build.
func ExtractGraph(args ...any) (*Graph, error) { return Build(args...) }

// ExtractMetadataGraph is the conventional positional entry point. It accepts
// the same argument forms as Build.
func ExtractMetadataGraph(args ...any) (*Graph, error) { return Build(args...) }

// BuildWithOptions is an explicit options entry point.
func BuildWithOptions(o Options) (*Graph, error) { return Build(o) }

// BuildGraphFromOptions is an alias for BuildWithOptions.
func BuildGraphFromOptions(o Options) (*Graph, error) { return Build(o) }

// BuildGraphFromScripts accepts raw script blocks directly.
func BuildGraphFromScripts(scripts any, rest ...any) (*Graph, error) {
	args := make([]any, 0, 1+len(rest))
	args = append(args, scripts)
	args = append(args, rest...)
	return Build(args...)
}

// Additional descriptive aliases used by extraction hosts.
func Analyze(args ...any) (*Graph, error)            { return Build(args...) }
func ExtractJSONLD(args ...any) (*Graph, error)      { return Build(args...) }
func BuildMetadataGraph(args ...any) (*Graph, error) { return Build(args...) }
func NewExtractor(args ...any) *Builder              { return NewBuilder(args...) }

// ProcessScripts is an alias for Build.
func ProcessScripts(args ...any) (*Graph, error) { return Build(args...) }

// BuildFromScripts is an alias for Build.
func BuildFromScripts(args ...any) (*Graph, error) { return Build(args...) }

// GraphFromScripts is an alias for Build.
func GraphFromScripts(args ...any) (*Graph, error) { return Build(args...) }

// ExtractMetadata is an alias for Build.
func ExtractMetadata(args ...any) (*Graph, error) { return Build(args...) }

// ProcessMetadata is an alias for Build.
func ProcessMetadata(args ...any) (*Graph, error) { return Build(args...) }

// NewGraph is an alias for NewBuilder.
func NewGraph(args ...any) *Builder { return NewBuilder(args...) }

// ExtractFromScripts is a typed convenience wrapper.
func ExtractFromScripts(scripts any, rest ...any) (*Graph, error) {
	return BuildGraphFromScripts(scripts, rest...)
}

// BuildGraphFromTrees accepts already decoded JSON trees.
func BuildGraphFromTrees(trees any, rest ...any) (*Graph, error) {
	return BuildGraphFromScripts(trees, rest...)
}

func normalizeTypeIRIs(values []string, defaults []string) []string {
	if len(values) == 0 {
		values = defaults
	}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if isAbsoluteIRI(value) {
			out = append(out, value)
			continue
		}
		if strings.HasPrefix(value, "schema:") {
			out = append(out, "https://schema.org/"+strings.TrimPrefix(value, "schema:"))
			continue
		}
		suffix := strings.TrimPrefix(value, "schema.org/")
		out = append(out, "https://schema.org/"+suffix)
	}
	if len(out) == 0 {
		out = append(out, defaults...)
	}
	return sortedTypes(out)
}

func normalizeSingleValued(values []string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
