package streamsel

// events receives a normalized stream of element open/close and text events.
// The parser guarantees that opens and closes are properly balanced: void and
// self-closing elements are closed immediately, implicitly closed elements
// are closed before the offending start tag, unmatched end tags are dropped,
// and all remaining open elements are closed at EOF.
type events interface {
	startTag(name string, attrs []attr)
	endTag(name string)
	text(s string)
}

// parser drives a tokenizer and turns raw tokens into balanced events,
// repairing unclosed tags, implicit ends and stray end tags on the fly.
type parser struct {
	tok   tokenizer
	stack []string
	ev    events
}

func (p *parser) feed(data []byte) {
	p.tok.feed(data)
	p.pump()
}

// pump pulls tokens one at a time so that handling a token (e.g. entering
// raw-text mode on <script>) takes effect before the next token is scanned.
func (p *parser) pump() {
	for len(p.tok.buf) > 0 {
		n, t, ok := p.tok.next()
		if !ok {
			break
		}
		p.tok.buf = p.tok.buf[n:]
		if t.typ == tokenText && t.text == "" {
			continue
		}
		p.handle(t)
	}
}

func (p *parser) close() {
	p.tok.eof = true
	p.pump()
	if len(p.tok.buf) > 0 {
		// Unterminated markup at EOF is treated as literal text.
		p.handle(token{typ: tokenText, text: decodeEntities(string(p.tok.buf))})
		p.tok.buf = nil
	}
	for len(p.stack) > 0 {
		p.ev.endTag(p.pop())
	}
}

func (p *parser) pop() string {
	n := len(p.stack) - 1
	name := p.stack[n]
	p.stack = p.stack[:n]
	return name
}

func (p *parser) handle(t token) {
	switch t.typ {
	case tokenText:
		if t.text != "" {
			p.ev.text(t.text)
		}
	case tokenStartTag:
		for len(p.stack) > 0 && closesImplicitly(t.name, p.stack[len(p.stack)-1]) {
			p.ev.endTag(p.pop())
		}
		p.ev.startTag(t.name, t.attrs)
		if voidElements[t.name] || t.selfClosing {
			p.ev.endTag(t.name)
			return
		}
		p.stack = append(p.stack, t.name)
		if rawTextElements[t.name] {
			p.tok.raw = t.name
		}
	case tokenEndTag:
		idx := -1
		for i := len(p.stack) - 1; i >= 0; i-- {
			if p.stack[i] == t.name {
				idx = i
				break
			}
		}
		if idx < 0 {
			return // stray end tag with no open element
		}
		for len(p.stack) > idx {
			p.ev.endTag(p.pop())
		}
	}
}

var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

var rawTextElements = map[string]bool{
	"script": true, "style": true, "textarea": true, "title": true,
}

// implicitClose[incoming] lists open tags that an incoming start tag closes.
var implicitClose = map[string]map[string]bool{}

func init() {
	add := func(incoming string, closes ...string) {
		m := implicitClose[incoming]
		if m == nil {
			m = map[string]bool{}
			implicitClose[incoming] = m
		}
		for _, c := range closes {
			m[c] = true
		}
	}
	add("li", "li", "p")
	add("dt", "dt", "dd", "p")
	add("dd", "dt", "dd", "p")
	add("rt", "rt", "rp")
	add("rp", "rt", "rp")
	add("option", "option")
	add("optgroup", "option", "optgroup")
	add("tr", "tr", "td", "th")
	add("td", "td", "th")
	add("th", "td", "th")
	add("thead", "tr", "td", "th")
	add("tbody", "tr", "td", "th", "thead")
	add("tfoot", "tr", "td", "th", "thead", "tbody")
	add("body", "head")
	// Block-level starts implicitly close an open <p>.
	for _, t := range []string{
		"address", "article", "aside", "blockquote", "details", "div", "dl",
		"fieldset", "figcaption", "figure", "footer", "form",
		"h1", "h2", "h3", "h4", "h5", "h6",
		"header", "hgroup", "hr", "main", "menu", "nav", "ol", "p", "pre",
		"section", "table", "ul",
	} {
		add(t, "p")
	}
}

func closesImplicitly(incoming, open string) bool {
	return implicitClose[incoming][open]
}
