package configcomplete

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
)

// Random generator of complete, valid documents. Ground truth is derived
// from the generated samples themselves (plus encoding/json as an
// independent oracle), never from hard-coded context tables.

var wsOptions = []string{"", " ", "  ", "\n", "\t", " \n "}

func genWS(r *rand.Rand) string { return wsOptions[r.Intn(len(wsOptions))] }

var strPieces = []string{
	"a", "Z", "0", "9", " ", "_", "-", ".", "~", "!", "@", "#", "%",
	`\n`, `\t`, `\r`, `\b`, `\f`, `\"`, `\\`, `\/`,
	`\u0041`, `\u00e9`, `\u4e16`, `\ud83d\ude00`, `\uABCD`,
	"é", "世", "🚀", "±", "€", "界",
}

func genString(r *rand.Rand) string {
	var b strings.Builder
	b.WriteByte('"')
	for n := r.Intn(7); n > 0; n-- {
		b.WriteString(strPieces[r.Intn(len(strPieces))])
	}
	b.WriteByte('"')
	return b.String()
}

var numOptions = []string{
	"0", "1", "-1", "7", "42", "123456", "0.5", "-3.25", "1e3", "1E+3",
	"2.5e-2", "-0", "10", "100", "6.022e23", "-0.001", "9007199254740991",
}

func genValue(r *rand.Rand, depth int) string {
	choice := r.Intn(10)
	if depth >= 4 {
		choice = r.Intn(5) // scalars only
	}
	switch {
	case choice == 0:
		return numOptions[r.Intn(len(numOptions))]
	case choice == 1 || choice == 2:
		return genString(r)
	case choice == 3:
		return []string{"true", "false"}[r.Intn(2)]
	case choice == 4:
		return "null"
	case choice < 8: // object
		var b strings.Builder
		b.WriteByte('{')
		b.WriteString(genWS(r))
		for i, n := 0, r.Intn(4); i < n; i++ {
			if i > 0 {
				b.WriteByte(',')
				b.WriteString(genWS(r))
			}
			b.WriteString(genString(r))
			b.WriteString(genWS(r))
			b.WriteByte(':')
			b.WriteString(genWS(r))
			b.WriteString(genValue(r, depth+1))
			b.WriteString(genWS(r))
		}
		b.WriteByte('}')
		return b.String()
	default: // array
		var b strings.Builder
		b.WriteByte('[')
		b.WriteString(genWS(r))
		for i, n := 0, r.Intn(5); i < n; i++ {
			if i > 0 {
				b.WriteByte(',')
				b.WriteString(genWS(r))
			}
			b.WriteString(genValue(r, depth+1))
			b.WriteString(genWS(r))
		}
		b.WriteByte(']')
		return b.String()
	}
}

func catOf(k tokKind) TokenCategory {
	switch k {
	case kLBrace:
		return CatObjectStart
	case kRBrace:
		return CatObjectEnd
	case kLBracket:
		return CatArrayStart
	case kRBracket:
		return CatArrayEnd
	case kColon:
		return CatColon
	case kComma:
		return CatComma
	case kString:
		return CatString
	case kNumber:
		return CatNumber
	case kTrue, kFalse:
		return CatBoolean
	case kNull:
		return CatNull
	}
	return ""
}

// actualCategory derives the expected category at offset k from the
// tokens of the complete document: the token starting at k if any, else
// the token containing k, else the next token after k.
func actualCategory(toks []token, k int) (TokenCategory, bool) {
	for _, t := range toks {
		if t.start == k {
			return catOf(t.kind), false
		}
	}
	for _, t := range toks {
		if t.start < k && k < t.end {
			return catOf(t.kind), false
		}
	}
	for _, t := range toks {
		if t.start > k {
			return catOf(t.kind), false
		}
	}
	return "", true // k is at/after the end: document complete
}

// lexComplete lexes a complete document, treating a trailing extendable
// number as a finished token.
func lexComplete(t *testing.T, doc string) []token {
	t.Helper()
	toks, open, lerr := lex(doc)
	if lerr != nil {
		t.Fatalf("own lexer rejected doc %q: %s at %d", doc, lerr.msg, lerr.off)
	}
	if open != nil {
		if open.kind != openNumber || open.needDigit {
			t.Fatalf("own lexer left open token %+v in doc %q", open, doc)
		}
		toks = append(toks, token{kNumber, open.start, len(doc)})
	}
	return toks
}

// bruteAlphabet contains one representative byte per lexical class.
var bruteAlphabet = []byte(`{}[]",:01.-+eEtrufalsnb\/ `)

// anyShorterValidSuffix searches exhaustively for a suffix shorter than l
// (enumeration is capped at length 3) that makes prefix valid JSON.
func anyShorterValidSuffix(prefix string, l int) (string, bool) {
	if l == 0 {
		return "", false
	}
	if l > 4 {
		l = 4
	}
	if json.Valid([]byte(prefix)) {
		return "", true
	}
	var found string
	var rec func(buf []byte, depth int)
	rec = func(buf []byte, depth int) {
		if found != "" {
			return
		}
		if depth == 0 {
			if json.Valid(append([]byte(prefix), buf...)) {
				found = string(buf)
			}
			return
		}
		for _, c := range bruteAlphabet {
			rec(append(buf, c), depth-1)
			if found != "" {
				return
			}
		}
	}
	for length := 1; length < l && found == ""; length++ {
		rec(nil, length)
	}
	return found, found != ""
}

func TestTruncationCrossCheck(t *testing.T) {
	r := rand.New(rand.NewSource(20261005))
	brute3, brute4 := 0, 0
	for sample := 0; sample < 400; sample++ {
		doc := genValue(r, 0)
		if !json.Valid([]byte(doc)) {
			t.Fatalf("generator produced invalid JSON: %q", doc)
		}
		toks := lexComplete(t, doc)
		for k := 0; k <= len(doc); k++ {
			res := Complete(doc, k)
			if !res.OK {
				t.Fatalf("Complete(%q, %d): unexpected error %q at %d", doc, k, res.Err, res.ErrOffset)
			}
			wantCat, done := actualCategory(toks, k)
			if done {
				if n := len(toks); n > 0 && toks[n-1].kind == kNumber && toks[n-1].end == k {
					// A number at the very end of the document may still
					// be extended (e.g. -0 -> -0.5), so the number
					// category stays legal with an empty suffix.
					if !sameCats(res.Categories, []TokenCategory{CatNumber}) || res.Suffix != "" {
						t.Fatalf("Complete(%q, %d): extendable number at end, got cats=%v suffix=%q", doc, k, res.Categories, res.Suffix)
					}
					continue
				}
				if len(res.Categories) != 0 || res.Suffix != "" {
					t.Fatalf("Complete(%q, %d): complete doc, got cats=%v suffix=%q", doc, k, res.Categories, res.Suffix)
				}
				continue
			}
			found := false
			for _, c := range res.Categories {
				if c == wantCat {
					found = true
				}
			}
			if !found {
				t.Fatalf("Complete(%q, %d): categories %v missing actual next-token category %q", doc, k, res.Categories, wantCat)
			}
			completed := doc[:k] + res.Suffix
			if !json.Valid([]byte(completed)) {
				t.Fatalf("Complete(%q, %d): suffix %q yields invalid document %q", doc, k, res.Suffix, completed)
			}
			// The completion must not modify valid tokens before the
			// cursor: every complete token of the prefix appears
			// identically at the start of the completed document.
			ctoks := lexComplete(t, completed)
			ptoks, _, _ := lex(doc[:k])
			if len(ctoks) < len(ptoks) {
				t.Fatalf("Complete(%q, %d): completed doc has fewer tokens than prefix", doc, k)
			}
			for i, pt := range ptoks {
				if ctoks[i] != pt {
					t.Fatalf("Complete(%q, %d): prefix token %d changed from %v to %v", doc, k, i, pt, ctoks[i])
				}
			}
			// Minimality: dropping the last byte must not stay valid...
			if len(res.Suffix) > 0 {
				shorter := doc[:k] + res.Suffix[:len(res.Suffix)-1]
				if json.Valid([]byte(shorter)) {
					t.Fatalf("Complete(%q, %d): suffix %q not minimal, %q also valid", doc, k, res.Suffix, shorter)
				}
			}
			// ...and for short suffixes, exhaustively confirm no shorter
			// suffix over the representative alphabet is valid.
			l := len(res.Suffix)
			switch {
			case l <= 2:
				if s, ok := anyShorterValidSuffix(doc[:k], l); ok {
					t.Fatalf("Complete(%q, %d): suffix %q not shortest, %q also works", doc, k, res.Suffix, s)
				}
			case l == 3 && brute3 < 500:
				brute3++
				if s, ok := anyShorterValidSuffix(doc[:k], l); ok {
					t.Fatalf("Complete(%q, %d): suffix %q not shortest, %q also works", doc, k, res.Suffix, s)
				}
			case l >= 4 && brute4 < 60:
				brute4++
				if s, ok := anyShorterValidSuffix(doc[:k], l); ok {
					t.Fatalf("Complete(%q, %d): suffix %q not shortest, %q also works", doc, k, res.Suffix, s)
				}
			}
		}
	}
	t.Logf("exhaustive minimality checks: %d of length 3, %d of length >= 4", brute3, brute4)
}

// FuzzComplete checks the core invariant on arbitrary inputs: whenever
// Complete reports success, prefix+suffix is valid JSON.
func FuzzComplete(f *testing.F) {
	f.Add(`{"a":[1,"x\n",null]}`, 5)
	f.Add(`["é\`, 4)
	f.Add("[-1.5e+", 6)
	f.Add("tru", 2)
	f.Fuzz(func(t *testing.T, s string, k int) {
		if k < 0 {
			k = -k
		}
		if k > len(s) {
			k = len(s)
		}
		res := Complete(s, k)
		if !res.OK {
			return
		}
		completed := s[:k] + res.Suffix
		if !json.Valid([]byte(completed)) {
			t.Errorf("Complete(%q, %d): suffix %q yields invalid document %q", s, k, res.Suffix, completed)
		}
	})
}
