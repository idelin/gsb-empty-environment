package configcomplete

// Verification strategy: instead of hand-guessing contexts from keyword
// tables, generate random complete documents and truncate them at every
// byte offset. For each cut the remaining text of the original document
// is a witness that the prefix is extendable, so the completion engine
// must not report a conflict; the token that actually follows the cut in
// the original document must be among the reported categories; and the
// shortest suffix must be valid, token-preserving and no longer than the
// original remainder.

import (
	"fmt"
	"math/rand"
	"strconv"
	"testing"
)

func genWS(r *rand.Rand) string {
	switch r.Intn(6) {
	case 0:
		return " "
	case 1:
		return "\n\t"
	case 2:
		return "\r\n "
	default:
		return ""
	}
}

func genString(r *rand.Rand) string {
	s := "\""
	n := r.Intn(9)
	for i := 0; i < n; i++ {
		switch r.Intn(12) {
		case 0:
			s += `\"`
		case 1:
			s += `\\`
		case 2:
			s += `\/`
		case 3:
			s += `\n`
		case 4:
			s += `\t`
		case 5:
			s += fmt.Sprintf(`\u%04x`, r.Intn(0x10000))
		case 6:
			s += "中"
		case 7:
			s += "🎉"
		case 8:
			s += " "
		default:
			s += string('a' + byte(r.Intn(26)))
		}
	}
	return s + "\""
}

func genNumber(r *rand.Rand) string {
	intPart := func() string {
		if r.Intn(4) == 0 {
			return "0"
		}
		return strconv.Itoa(1 + r.Intn(99999))
	}
	num := ""
	if r.Intn(4) == 0 {
		num = "-"
	}
	num += intPart()
	if r.Intn(3) == 0 {
		num += "." + strconv.Itoa(r.Intn(1000000))
	}
	if r.Intn(3) == 0 {
		e := "e"
		if r.Intn(2) == 0 {
			e = "E"
		}
		num += e
		switch r.Intn(3) {
		case 0:
			num += "+"
		case 1:
			num += "-"
		}
		num += strconv.Itoa(r.Intn(300))
	}
	return num
}

func genValue(r *rand.Rand, depth int) string {
	kind := r.Intn(6)
	if depth >= 4 {
		kind = 2 + r.Intn(4)
	}
	switch kind {
	case 0:
		n := r.Intn(4)
		s := "{" + genWS(r)
		for i := 0; i < n; i++ {
			if i > 0 {
				s += "," + genWS(r)
			}
			s += genString(r) + genWS(r) + ":" + genWS(r) + genValue(r, depth+1) + genWS(r)
		}
		return s + "}"
	case 1:
		n := r.Intn(5)
		s := "[" + genWS(r)
		for i := 0; i < n; i++ {
			if i > 0 {
				s += "," + genWS(r)
			}
			s += genValue(r, depth+1) + genWS(r)
		}
		return s + "]"
	case 2:
		return genString(r)
	case 3:
		return genNumber(r)
	case 4:
		if r.Intn(2) == 0 {
			return "true"
		}
		return "false"
	default:
		return "null"
	}
}

func genDoc(r *rand.Rand) string {
	doc := genValue(r, 0)
	if r.Intn(3) == 0 {
		doc = genWS(r) + doc
	}
	if r.Intn(3) == 0 {
		doc += genWS(r)
	}
	return doc
}

// classifyStringAt reports which intra-string category begins at byte
// offset cut inside the string token tok of document doc.
func classifyStringAt(doc string, tok token, cut int) TokenCategory {
	i := tok.start + 1
	for i < tok.end-1 {
		if doc[i] == '\\' {
			escEnd := i + 2
			if doc[i+1] == 'u' {
				escEnd = i + 6
			}
			if cut == i {
				return CatEscape
			}
			if cut > i && cut < escEnd {
				if doc[i+1] == 'u' && cut >= i+2 {
					return CatEscapeHex
				}
				return CatEscape
			}
			i = escEnd
			continue
		}
		if cut == i {
			return CatStringChar
		}
		i++
	}
	return CatStringEnd
}

func containsCat(cats []TokenCategory, c TokenCategory) bool {
	for _, x := range cats {
		if x == c {
			return true
		}
	}
	return false
}

func checkCut(t *testing.T, doc string, cut int) {
	prefix := doc[:cut]
	remainder := doc[cut:]
	res := Complete(prefix)
	if res.Conflict != nil {
		t.Fatalf("doc %q cut %d: unexpected conflict at %d: %s", doc, cut, res.Conflict.Pos, res.Conflict.Msg)
	}
	if !Valid(prefix + res.Suffix) {
		t.Fatalf("doc %q cut %d: prefix+suffix %q is not a valid document", doc, cut, prefix+res.Suffix)
	}
	if len(res.Suffix) > len(remainder) {
		t.Fatalf("doc %q cut %d: suffix %q longer than original remainder %q", doc, cut, res.Suffix, remainder)
	}
	prefixLex := lex(prefix)
	joined := lex(prefix + res.Suffix)
	if joined.conflict >= 0 || joined.partial != nil {
		t.Fatalf("doc %q cut %d: prefix+suffix does not lex cleanly", doc, cut)
	}
	if len(joined.tokens) < len(prefixLex.tokens) {
		t.Fatalf("doc %q cut %d: suffix removed tokens", doc, cut)
	}
	for i, pt := range prefixLex.tokens {
		if joined.tokens[i] != pt {
			t.Fatalf("doc %q cut %d: token %d changed from %+v to %+v", doc, cut, i, pt, joined.tokens[i])
		}
	}
	toks := lex(doc).tokens
	var tok *token
	for i := range toks {
		if cut < toks[i].end {
			tok = &toks[i]
			break
		}
	}
	switch {
	case tok == nil:
		if len(res.Categories) != 0 || res.Suffix != "" {
			t.Fatalf("doc %q cut %d: past end, got categories %v suffix %q", doc, cut, res.Categories, res.Suffix)
		}
	case tok.start < cut && cut < tok.end:
		switch tok.cat {
		case CatString:
			want := classifyStringAt(doc, *tok, cut)
			if !containsCat(res.Categories, want) {
				t.Fatalf("doc %q cut %d: inside string, categories %v missing %v", doc, cut, res.Categories, want)
			}
		case CatNumber:
			if prefixLex.partial != nil && !containsCat(res.Categories, CatNumber) {
				t.Fatalf("doc %q cut %d: inside partial number, categories %v missing number", doc, cut, res.Categories)
			}
		case CatTrue, CatFalse, CatNull:
			if !containsCat(res.Categories, tok.cat) {
				t.Fatalf("doc %q cut %d: inside keyword, categories %v missing %v", doc, cut, res.Categories, tok.cat)
			}
		}
	default:
		if !containsCat(res.Categories, tok.cat) {
			t.Fatalf("doc %q cut %d: categories %v missing next token %v", doc, cut, res.Categories, tok.cat)
		}
	}
}

func TestTruncateGeneratedDocs(t *testing.T) {
	r := rand.New(rand.NewSource(20261005))
	for iter := 0; iter < 3000; iter++ {
		doc := genDoc(r)
		if !Valid(doc) {
			t.Fatalf("generator produced invalid document: %q", doc)
		}
		for cut := 0; cut <= len(doc); cut++ {
			checkCut(t, doc, cut)
		}
	}
}

func TestTruncateHandpickedDocs(t *testing.T) {
	docs := []string{
		`{"a":{"b":[1,2.5e-3,{"c":"d\u00e9f"}],"e":"\n\t\\"},"f":[true,false,null],"g":{}}`,
		`[[[["deep"]]],{"k":"🎉中"}]`,
		`0`,
		`""`,
		`{"":""}`,
		`[-0.0E+0]`,
		"{\r\n\t\"key\" : [ 1 , 2 ] }",
	}
	for _, doc := range docs {
		if !Valid(doc) {
			t.Fatalf("handpicked document invalid: %q", doc)
		}
		for cut := 0; cut <= len(doc); cut++ {
			checkCut(t, doc, cut)
		}
	}
}
