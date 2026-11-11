package streamsel

import (
	"reflect"
	"sort"
	"testing"
)

// collect runs the streaming matcher over the given chunks and gathers all
// emitted texts.
func collect(t *testing.T, sel string, chunks ...string) []string {
	t.Helper()
	m, err := NewMatcher(sel)
	if err != nil {
		t.Fatalf("NewMatcher(%q): %v", sel, err)
	}
	var out []string
	for _, c := range chunks {
		out = append(out, m.Write([]byte(c))...)
	}
	out = append(out, m.Close()...)
	return out
}

func sorted(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}

func TestMatcherCases(t *testing.T) {
	cases := []struct {
		name string
		html string
		sel  string
		want []string
	}{
		{"tag", `<p>a</p><p>b</p>`, "p", []string{"a", "b"}},
		{"class", `<div class="x y">one</div><div class="y">two</div>`, ".x", []string{"one"}},
		{"id", `<div id="a">x</div><div id="b">y</div>`, "#b", []string{"y"}},
		{"attr presence", `<input disabled><input type=text>`, "[disabled]", []string{""}},
		{"attr value", `<a href="u1">l1</a><a href="u2">l2</a>`, `[href="u2"]`, []string{"l2"}},
		{"tag.class", `<span class="a">1</span><b class="a">2</b>`, "span.a", []string{"1"}},
		{"universal", `<div><span>s</span></div>`, "div *", []string{"s"}},
		{"descendant", `<div><section><span>deep</span></section></div>`, "div span", []string{"deep"}},
		{"child", `<div><span>in</span></div><section><p><span>out</span></p></section>`, "div > span", []string{"in"}},
		{"child not grandchild", `<div><p><span>x</span></p></div>`, "div > span", nil},
		{"chain", `<ul><li><a href="h">go</a></li></ul>`, "ul > li > a", []string{"go"}},
		{"mixed combinators", `<div><b><i><span>hit</span></i></b></div>`, "div > b span", []string{"hit"}},
		{"nested matches", `<div>a<div>b</div></div>`, "div", []string{"ab", "b"}},
		{"unclosed at EOF", `<div><span>open`, "span", []string{"open"}},
		{"implicit li", `<ul><li>one<li>two<li>three</ul>`, "li", []string{"one", "two", "three"}},
		{"implicit p", `<p>first<p>second`, "p", []string{"first", "second"}},
		{"implicit table cells", `<table><tr><td>a<td>b<tr><td>c<td>d</table>`, "td", []string{"a", "b", "c", "d"}},
		{"implicit dt dd", `<dl><dt>t1<dd>d1<dt>t2<dd>d2</dl>`, "dd", []string{"d1", "d2"}},
		{"self closing", `<div><br/>after</div>`, "div", []string{"after"}},
		{"void elements", `<div><img src="a.png"><hr>tail</div>`, "div", []string{"tail"}},
		{"stray end tag", `<div>a</b>b</div>`, "div", []string{"ab"}},
		{"end tag closes ancestors", `<div><span><b>x</div>`, "b", []string{"x"}},
		{"script raw text", `<div><script>if (a<b) { x("</span>"); }</script>tail</div>`, "div",
			[]string{`if (a<b) { x("</span>"); }tail`}},
		{"comment skipped", `<div><!-- <span>no</span> --><span>yes</span></div>`, "span", []string{"yes"}},
		{"entities", `<p>a &amp; b &lt; c &#65; &#x42;</p>`, "p", []string{"a & b < c A B"}},
		{"attr with gt", `<div data-x="a>b" class="hit">t</div>`, ".hit", []string{"t"}},
		{"attr single quotes", `<a href='u'>l</a>`, `[href='u']`, []string{"l"}},
		{"case insensitive tags", `<DIV CLASS="a">x</DIV>`, "div.a", []string{"x"}},
		{"doctype and pi", `<!DOCTYPE html><?xml v?><p>z</p>`, "p", []string{"z"}},
		{"text with stray lt", `<p>1 < 2 and 3 > 2</p>`, "p", []string{"1 < 2 and 3 > 2"}},
		{"unicode", `<p>你好，世界</p>`, "p", []string{"你好，世界"}},
		{"option implicit", `<select><option>a<option>b</select>`, "option", []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := collect(t, tc.sel, tc.html)
			if !reflect.DeepEqual(sorted(got), sorted(tc.want)) {
				t.Fatalf("streaming got %q, want %q", got, tc.want)
			}
			root := ParseDOM(tc.html)
			naive, err := QueryAll(root, tc.sel)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(sorted(naive), sorted(tc.want)) {
				t.Fatalf("naive got %q, want %q", naive, tc.want)
			}
		})
	}
}

// TestMatcherVsNaive cross-checks streaming and tree matching on a larger
// document with many selectors.
func TestMatcherVsNaive(t *testing.T) {
	html := `<!DOCTYPE html><html><head><title>t</title></head><body>
<div id="main" class="wrap wide">
  <ul class="list">
    <li class="item" data-id="1"><span class="price">$1</span><p>first
    <li class="item sale" data-id="2"><span class="price">$2</span><p>second
    <li class="item" data-id="3"><span class="price">$3</span><p>third
  </ul>
  <table><tr><td>h1<th>h2<tr><td>a<td>b<tr><td>c<td>d</table>
  <div class="footer"><p>done &amp; dusted</p></div>
</div>
<script>var s = "<li>not real</li>";</script>
</body></html>`
	sels := []string{
		"li", ".item", "li.item", ".item.sale", "#main", `[data-id="2"]`, "[data-id]",
		"ul.list > li", "div li", "#main .price", "td", "th", "table td",
		"div.footer p", ".wrap .footer > p", "span", "p", "title", "script",
		"body > div", "html body div", "*", "ul > li > span.price",
	}
	root := ParseDOM(html)
	for _, sel := range sels {
		got := collect(t, sel, html)
		want, err := QueryAll(root, sel)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(sorted(got), sorted(want)) {
			t.Errorf("selector %q: streaming=%q naive=%q", sel, got, want)
		}
	}
}

// enumDoc exercises every tokenizer and repair feature so that chunk
// boundaries can hit any parser state.
const enumDoc = `<!DOCTYPE html><html><body>
<div id="main" class="wrap">
<ul><li class="item" data-n="1">一 &amp; 二<li class="item">三<b>四</b>
<li class="item hot">五</ul>
<table><tr><td>a<td>b<tr><td>c<td>d</table>
<p>para &lt;one&gt;<p>第二段 &#20320;
<div data-x="a>b"><img src="i.png"/><br/>tail</div>
<script>if (a<b) { s = "</p>"; }</script>
<!-- comment <li>fake</li> -->
<span title='单 "引号" &amp; 实体'>末</span>
</body></html>`

var enumSels = []string{
	"li", "li.item", ".hot", "td", "p", "div > span", "#main li", `[data-n="1"]`,
	"ul.list li", "b", "span[title]", "body p", "html body > div",
}

// TestChunkEnumerationTwoChunks splits the document at every possible byte
// position and verifies the output is identical to single-shot feeding.
func TestChunkEnumerationTwoChunks(t *testing.T) {
	for _, sel := range enumSels {
		base := collect(t, sel, enumDoc)
		for cut := 0; cut <= len(enumDoc); cut++ {
			got := collect(t, sel, enumDoc[:cut], enumDoc[cut:])
			if !reflect.DeepEqual(got, base) {
				t.Fatalf("sel %q cut %d: got %q, want %q", sel, cut, got, base)
			}
		}
	}
}

// TestChunkEnumerationThreeChunks enumerates all pairs of cut positions over
// a shorter document, feeding three chunks.
func TestChunkEnumerationThreeChunks(t *testing.T) {
	doc := `<ul><li>a &amp; b<li>c</ul><p>x<p>y<br/><img src="z">`
	for _, s := range []string{"li", "p", "ul > li"} {
		base := collect(t, s, doc)
		for i := 0; i <= len(doc); i++ {
			for j := i; j <= len(doc); j++ {
				got := collect(t, s, doc[:i], doc[i:j], doc[j:])
				if !reflect.DeepEqual(got, base) {
					t.Fatalf("sel %q cuts (%d,%d): got %q, want %q", s, i, j, got, base)
				}
			}
		}
	}
}

// TestChunkEnumerationByteAtATime feeds the document one byte at a time.
func TestChunkEnumerationByteAtATime(t *testing.T) {
	for _, sel := range enumSels {
		base := collect(t, sel, enumDoc)
		m, err := NewMatcher(sel)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for i := 0; i < len(enumDoc); i++ {
			got = append(got, m.Write([]byte(enumDoc[i:i+1]))...)
		}
		got = append(got, m.Close()...)
		if !reflect.DeepEqual(got, base) {
			t.Fatalf("sel %q byte-at-a-time: got %q, want %q", sel, got, base)
		}
	}
}

func TestSelectorErrors(t *testing.T) {
	for _, sel := range []string{"", "  ", ">", "div >", ".", "#", "[]", "[=x]", "[x", "div,", "div > > p"} {
		if _, err := NewMatcher(sel); err == nil {
			t.Errorf("NewMatcher(%q) should fail", sel)
		}
	}
	for _, sel := range []string{"div", ".a", "#i", "[a]", `[a="v"]`, "[a=v]", "div.a#i[x][y='z'] > span *", "*"} {
		if _, err := NewMatcher(sel); err != nil {
			t.Errorf("NewMatcher(%q) should succeed: %v", sel, err)
		}
	}
}
