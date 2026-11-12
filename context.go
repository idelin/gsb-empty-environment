package taskpkg

import (
	"net/url"
	"sort"
	"strings"
)

type termDefinition struct {
	mapping      string
	resolved     string
	typeMapping  string
	container    string
	language     string
	reverse      bool
	contextValue any
}

type activeContext struct {
	terms       map[string]*termDefinition
	vocab       string
	base        string
	initialBase string
	language    string
}

func newContext(base string) *activeContext {
	return &activeContext{
		terms:       make(map[string]*termDefinition),
		base:        base,
		initialBase: base,
	}
}

func (c *activeContext) clone() *activeContext {
	out := &activeContext{
		terms:       make(map[string]*termDefinition, len(c.terms)),
		vocab:       c.vocab,
		base:        c.base,
		initialBase: c.initialBase,
		language:    c.language,
	}
	for k, v := range c.terms {
		out.terms[k] = v
	}
	return out
}

func (c *activeContext) reset() *activeContext {
	return newContext(c.initialBase)
}

func isKeyword(value string) bool {
	switch value {
	case "@id", "@type", "@value", "@language", "@list", "@set", "@graph",
		"@context", "@reverse", "@included", "@index", "@nest", "@base",
		"@vocab", "@container", "@direction", "@prefix", "@protected",
		"@version", "@import", "@json", "@none":
		return true
	}
	return false
}

// processContext applies a local context and returns a new active context.
// The receiver is never mutated, which naturally restores the enclosing
// context when a nested JSON object is left.
func (c *activeContext) processContext(raw any, script int, path string) (*activeContext, []UnresolvedContext) {
	if raw == nil {
		return c.reset(), nil
	}
	switch value := raw.(type) {
	case string:
		if value == "" {
			return c.clone(), nil
		}
		// A remote context replaces the active context. Since it cannot be
		// dereferenced here, return an unresolved marker and continue with
		// only the document's initial base; inherited term definitions must
		// not leak into a scope whose real context is unknown.
		return c.reset(), []UnresolvedContext{{
			Context: value,
			Script:  script,
			Path:    path,
			Reason:  "remote context was not resolved; network access disabled",
		}}
	case []any:
		ctx := c.clone()
		var unresolved []UnresolvedContext
		for _, item := range value {
			var more []UnresolvedContext
			ctx, more = ctx.processContext(item, script, path)
			unresolved = append(unresolved, more...)
		}
		return ctx, unresolved
	case map[string]any:
		ctx := c.clone()
		var unresolved []UnresolvedContext

		if importValue, ok := value["@import"]; ok {
			if remote, ok := importValue.(string); ok && remote != "" {
				unresolved = append(unresolved, UnresolvedContext{
					Context: remote,
					Script:  script,
					Path:    path,
					Reason:  "remote @import was not resolved; network access disabled",
				})
			}
		}

		if baseValue, ok := value["@base"]; ok {
			switch b := baseValue.(type) {
			case nil:
				ctx.base = ""
			case string:
				ctx.base = ctx.resolveAgainstBase(b)
			}
		}

		if vocabValue, ok := value["@vocab"]; ok {
			switch v := vocabValue.(type) {
			case nil:
				ctx.vocab = ""
			case string:
				extra := make(map[string]*termDefinition)
				for term, rawDef := range value {
					if strings.HasPrefix(term, "@") {
						continue
					}
					switch def := rawDef.(type) {
					case string:
						extra[term] = &termDefinition{mapping: def}
					case map[string]any:
						if id, ok := def["@id"].(string); ok {
							extra[term] = &termDefinition{mapping: id}
						}
					}
				}
				ctx.vocab = ctx.expandContextIRI(v, extra)
			}
		}

		if langValue, ok := value["@language"]; ok {
			switch l := langValue.(type) {
			case nil:
				ctx.language = ""
			case string:
				ctx.language = l
			}
		}

		keys := make([]string, 0, len(value))
		for k := range value {
			if k == "@context" || strings.HasPrefix(k, "@") {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, term := range keys {
			defRaw := value[term]
			switch def := defRaw.(type) {
			case nil:
				delete(ctx.terms, term)
			case string:
				ctx.terms[term] = &termDefinition{mapping: def}
			case map[string]any:
				if def["@id"] == nil {
					if _, hasID := def["@id"]; hasID {
						delete(ctx.terms, term)
						continue
					}
				}
				td := &termDefinition{}
				if id, ok := def["@id"].(string); ok {
					td.mapping = id
				}
				if typ, ok := def["@type"].(string); ok {
					td.typeMapping = typ
				}
				if container, ok := def["@container"].(string); ok {
					td.container = container
				}
				if language, ok := def["@language"].(string); ok {
					td.language = language
				}
				if reverse, ok := def["@reverse"].(bool); ok {
					td.reverse = reverse
				}
				if scoped, ok := def["@context"]; ok {
					td.contextValue = scoped
				}
				if td.mapping == "" && td.reverse {
					if id, ok := def["@reverse"].(string); ok {
						td.mapping = id
					}
				}
				if td.mapping == "" {
					td.mapping = term
				}
				ctx.terms[term] = td
			default:
				// Invalid term definitions are ignored rather than guessed.
			}
		}
		// Resolve each newly declared mapping against the context in which
		// it was declared. Inherited definitions therefore keep their
		// original meaning when a nested @base is later restored.
		for _, term := range keys {
			if td, ok := ctx.terms[term]; ok && td != nil && td.resolved == "" {
				td.resolved = ctx.expandTermMapping(td.mapping, 0)
			}
		}
		return ctx, unresolved
	default:
		return c.clone(), nil
	}
}

func (c *activeContext) term(name string) (*termDefinition, bool) {
	if c == nil {
		return nil, false
	}
	td, ok := c.terms[name]
	return td, ok
}

func (c *activeContext) expandTermMapping(mapping string, depth int) string {
	if mapping == "" || isKeyword(mapping) {
		return mapping
	}
	if depth > 32 {
		return mapping
	}
	if td, ok := c.term(mapping); ok {
		if td.resolved != "" {
			return td.resolved
		}
		if td.mapping == mapping {
			if c.vocab != "" {
				return c.vocab + mapping
			}
			return c.resolveAgainstBase(mapping)
		}
		return c.expandTermMapping(td.mapping, depth+1)
	}
	if prefix, suffix, ok := splitCompactIRI(mapping); ok {
		if td, found := c.term(prefix); found {
			expandedPrefix := c.expandTermMapping(td.mapping, depth+1)
			if expandedPrefix != "" && !isKeyword(expandedPrefix) {
				return expandedPrefix + suffix
			}
		}
		return mapping
	}
	if isAbsoluteIRI(mapping) {
		return mapping
	}
	if c.vocab != "" {
		return c.vocab + mapping
	}
	return c.resolveAgainstBase(mapping)
}

func (c *activeContext) expandContextIRI(value string, extra map[string]*termDefinition) string {
	if value == "" || isKeyword(value) {
		return value
	}
	if extra != nil {
		if td, found := extra[value]; found {
			return c.expandTermMapping(td.mapping, 0)
		}
	}
	if td, found := c.term(value); found {
		return c.expandTermMapping(td.mapping, 0)
	}
	if prefix, suffix, ok := splitCompactIRI(value); ok {
		if extra != nil {
			if td, found := extra[prefix]; found {
				return c.expandTermMapping(td.mapping, 0) + suffix
			}
		}
		if td, found := c.term(prefix); found {
			return c.expandTermMapping(td.mapping, 0) + suffix
		}
		return value
	}
	if isAbsoluteIRI(value) {
		return value
	}
	return c.resolveAgainstBase(value)
}

// expandIRI applies term/prefix/@vocab/@base rules. vocab selects property,
// type and term interpretation; documentRelative selects @id interpretation.
func (c *activeContext) expandIRI(value string, vocab, documentRelative bool) string {
	if value == "" {
		return ""
	}
	if isKeyword(value) {
		return value
	}
	if vocab {
		if td, ok := c.term(value); ok {
			return c.expandTermMapping(td.mapping, 0)
		}
	}
	if prefix, suffix, ok := splitCompactIRI(value); ok {
		if td, found := c.term(prefix); found {
			expandedPrefix := c.expandTermMapping(td.mapping, 0)
			if expandedPrefix != "" && !isKeyword(expandedPrefix) {
				return expandedPrefix + suffix
			}
		}
		return value
	}
	if vocab && c.vocab != "" {
		return c.vocab + value
	}
	if documentRelative {
		return c.resolveAgainstBase(value)
	}
	return value
}

func (c *activeContext) resolveAgainstBase(value string) string {
	if value == "" {
		return ""
	}
	if isAbsoluteIRI(value) || strings.HasPrefix(value, "_:") {
		return value
	}
	if c.base == "" {
		if strings.HasPrefix(value, "#") || strings.HasPrefix(value, "/") ||
			strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") {
			return value
		}
		return value
	}
	base, err := url.Parse(c.base)
	if err != nil {
		return value
	}
	ref, err := url.Parse(value)
	if err != nil {
		return value
	}
	return base.ResolveReference(ref).String()
}

func splitCompactIRI(value string) (string, string, bool) {
	idx := strings.IndexByte(value, ':')
	if idx <= 0 {
		return "", "", false
	}
	prefix := value[:idx]
	if strings.ContainsAny(prefix, "/?#") {
		return "", "", false
	}
	return prefix, value[idx+1:], true
}

func isAbsoluteIRI(value string) bool {
	if value == "" {
		return false
	}
	idx := strings.IndexByte(value, ':')
	if idx <= 0 {
		return false
	}
	scheme := value[:idx]
	if scheme == "_" {
		return true
	}
	for i, r := range scheme {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			continue
		}
		if i > 0 && (r >= '0' && r <= '9' || r == '-' || r == '+' || r == '.') {
			continue
		}
		return false
	}
	return true
}
