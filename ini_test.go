package netsec

import (
	"bytes"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func mustMarshal(t *testing.T, cfg *Config) string {
	t.Helper()
	out, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	return string(out)
}

func TestWriteRejectsInjection(t *testing.T) {
	badKeys := []string{
		"a=b", "a:b", "a;b", "a#b",
		"line\nbreak", "carriage\rreturn",
		" leading", "trailing ", "\ttab", "tab\t",
		"[bracket",
		"nul\x00key", "",
	}
	for _, key := range badKeys {
		cfg := NewConfig()
		if err := cfg.Set("app", key, "v"); err == nil {
			t.Errorf("Set with key %q expected error, got nil", key)
		}
	}

	badSections := []string{"admin]", "line\nbreak", "cr\rname", "nul\x00"}
	for _, section := range badSections {
		cfg := NewConfig()
		if err := cfg.Set(section, "k", "v"); err == nil {
			t.Errorf("Set with section %q expected error, got nil", section)
		}
	}

	cfg := NewConfig()
	if err := cfg.Set("app", "k", "with\x00nul"); err == nil {
		t.Error("Set with NUL in value expected error, got nil")
	}

	// 非法输入绝不能出现在序列化结果里。
	out := mustMarshal(t, NewConfig())
	if out != "" {
		t.Fatalf("empty config serialized to %q", out)
	}

	// 内部含 [ 的键仍然合法，且能够正常往返。
	cfg = NewConfig()
	if err := cfg.Set("s", "a[b]c", "v"); err != nil {
		t.Fatalf("internal brackets should be allowed: %v", err)
	}
	parsed, err := Unmarshal([]byte(mustMarshal(t, cfg)))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := parsed.Get("s", "a[b]c"); !ok || got != "v" {
		t.Fatalf("internal bracket key round trip failed: got %q ok=%v", got, ok)
	}
}

func TestWriteQuotesDangerousValues(t *testing.T) {
	cfg := NewConfig()
	if err := cfg.Set("s", "multiline", "line1\nline2\r\nline3"); err != nil {
		t.Fatal(err)
	}
	out := mustMarshal(t, cfg)
	want := "[s]\nmultiline = \"line1\\nline2\\r\\nline3\"\n"
	if out != want {
		t.Fatalf("multiline quoting mismatch:\n got: %q\nwant: %q", out, want)
	}

	quoted := []string{
		"[admin]", ";comment", "#hash",
		" leading", "trailing ", " both ",
		`quote"inside`, `back\slash`,
	}
	for _, value := range quoted {
		cfg := NewConfig()
		if err := cfg.Set("s", "k", value); err != nil {
			t.Fatalf("Set value %q failed: %v", value, err)
		}
		line := strings.TrimPrefix(mustMarshal(t, cfg), "[s]\n")
		if !strings.HasPrefix(line, `k = "`) {
			t.Errorf("value %q was not quoted, output: %q", value, line)
		}
	}

	cfg = NewConfig()
	if err := cfg.Set("s", "plain", "plain value"); err != nil {
		t.Fatal(err)
	}
	if got := mustMarshal(t, cfg); got != "[s]\nplain = plain value\n" {
		t.Fatalf("plain value should stay unquoted, got %q", got)
	}
}

func TestNewlineSectionInjectionNeutralized(t *testing.T) {
	attack := "good\n[admin]\nrole = root"
	cfg := NewConfig()
	if err := cfg.Set("app", "note", attack); err != nil {
		t.Fatal(err)
	}
	out := mustMarshal(t, cfg)

	// 整个攻击载荷必须位于同一行的双引号内，裸 [admin] 节头不得出现。
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "[") && line != "[app]" {
			t.Fatalf("injected section header leaked into output: %q", line)
		}
	}

	parsed, err := Unmarshal([]byte(out))
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	got, ok := parsed.Get("app", "note")
	if !ok {
		t.Fatal("key note disappeared after round trip")
	}
	if got != attack {
		t.Fatalf("attack payload altered:\n got: %q\nwant: %q", got, attack)
	}
	if _, ok := parsed.Get("admin", "role"); ok {
		t.Fatal("injected admin section was materialized during parse")
	}
}

func TestParseRestoresEscapes(t *testing.T) {
	cfg := NewConfig()
	original := "line1\nline2\rcarriage\ttab\"quote\\back"
	if err := cfg.Set("s", "k", original); err != nil {
		t.Fatal(err)
	}
	out := mustMarshal(t, cfg)

	parsed, err := Unmarshal([]byte(out))
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	got, ok := parsed.Get("s", "k")
	if !ok || got != original {
		t.Fatalf("escape restoration mismatch:\n got: %q\nwant: %q", got, original)
	}

	// 未知转义按字面形式保留，不擅自发明语义。
	hand, err := Unmarshal([]byte(`k = "a\tb"` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := hand.Get("", "k"); got != `a\tb` {
		t.Fatalf("unknown escape should stay literal, got %q", got)
	}

	// 引号内部的注释符是值的一部分，不能截断值。
	inline, err := Unmarshal([]byte(`k = "a ; b # c"` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := inline.Get("", "k"); got != "a ; b # c" {
		t.Fatalf("inline comment chars inside quotes got %q", got)
	}
}

func TestContinuationDoesNotCrossSectionHeader(t *testing.T) {
	// 正常续行：物理行被拼接成一个逻辑行。
	normal, err := Unmarshal([]byte("[s]\na = foo\\\nbar\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := normal.Get("s", "a"); got != "foobar" {
		t.Fatalf("normal continuation got %q, want foobar", got)
	}

	// 攻击/异常场景：续行遇到节头立即终止，节头保持独立。
	blocked, err := Unmarshal([]byte("[s]\na = foo\\\n[admin]\nb = bar\n"))
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	got, ok := blocked.Get("s", "a")
	if !ok || got != `foo\` {
		t.Fatalf("interrupted continuation got %q, want literal foo\\", got)
	}
	if got, ok := blocked.Get("admin", "b"); !ok || got != "bar" {
		t.Fatalf("section header after continuation not parsed, got %q ok=%v", got, ok)
	}

	// 带前导空白的节头同样必须独立成节，不能被续行吞掉。
	indented, err := Unmarshal([]byte("[s]\na = foo\\\n  [admin]\nb = bar\n"))
	if err != nil {
		t.Fatalf("indented header unmarshal failed: %v", err)
	}
	if got, _ := indented.Get("s", "a"); got != `foo\` {
		t.Fatalf("indented header continuation got %q, want foo\\", got)
	}
	if _, ok := indented.Get("admin", "b"); !ok {
		t.Fatal("indented section header was swallowed by continuation")
	}
	var names []string
	for _, s := range blocked.Sections {
		names = append(names, s.Name)
	}
	wantNames := []string{"", "s", "admin"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("section order mismatch: got %v, want %v", names, wantNames)
	}
}

func TestRoundTrip(t *testing.T) {
	cfg := NewConfig()
	values := map[string]string{
		"plain":     "hello world",
		"newline":   "a\nb",
		"crlf":      "a\r\nb",
		"leading":   "[section-like",
		"semicolon": ";tail",
		"hash":      "#tail",
		"spaces":    "  padded  ",
		"quotes":    `say "hi"`,
		"backslash": `C:\path\to`,
		"unicode":   "中文\u3000值",
		"empty":     "",
	}
	for key, value := range values {
		if err := cfg.Set("main", key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := cfg.Set("", "root.key", "root value"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("other", "x", "y\nz"); err != nil {
		t.Fatal(err)
	}

	first, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("first Marshal failed: %v", err)
	}
	parsed, err := Unmarshal(first)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if !reflect.DeepEqual(parsed, cfg) {
		t.Fatalf("parse(write(x)) != x\n got: %+v\nwant: %+v", parsed, cfg)
	}
	for key, value := range values {
		if got, ok := parsed.Get("main", key); !ok || got != value {
			t.Fatalf("key %q round trip got %q ok=%v, want %q", key, got, ok, value)
		}
	}

	second, err := Marshal(parsed)
	if err != nil {
		t.Fatalf("second Marshal failed: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("marshalling is not idempotent:\n first:  %q\n second: %q", first, second)
	}
}

func TestUnmarshalRejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"unterminated section":  "[broken\nk=v\n",
		"missing separator":     "no-separator-line\n",
		"unterminated quote":    `k = "abc` + "\n",
		"garbage after quote":   `k = "abc"def` + "\n",
		"section trailing junk": "[s] junk\n",
	}
	for name, input := range cases {
		if _, err := Unmarshal([]byte(input)); err == nil {
			t.Errorf("%s: expected error for input %q", name, input)
		}
	}
}

func TestMarshalRejectsHandBuiltInvalidConfig(t *testing.T) {
	cases := []*Config{
		{Sections: []Section{{Name: "bad]name", Pairs: []KeyValue{{Key: "k", Value: "v"}}}}},
		{Sections: []Section{{Name: "s", Pairs: []KeyValue{{Key: "a=b", Value: "v"}}}}},
		{Sections: []Section{{Name: "s", Pairs: []KeyValue{{Key: "k", Value: "nul\x00"}}}}},
	}
	for _, cfg := range cases {
		if _, err := Marshal(cfg); err == nil {
			t.Errorf("Marshal should reject hand-built config %+v", cfg)
		}
	}
}

func TestCRLFInput(t *testing.T) {
	cfg, err := Unmarshal([]byte("[s]\r\nk = v\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := cfg.Get("s", "k"); !ok || got != "v" {
		t.Fatalf("CRLF parse got %q ok=%v, want v", got, ok)
	}
}

func TestRandomRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(0x0583))
	keyAlphabet := []rune("abcABC012 []_-.,中文\t")
	sectionAlphabet := []rune("secSEC012 _-.[中文\t")
	valueAlphabet := []rune("ab [];#:=\\\"'\r\n\t 中文\x60")

	randString := func(alphabet []rune, maxLen int) string {
		var b strings.Builder
		for i := rng.Intn(maxLen); i > 0; i-- {
			b.WriteRune(alphabet[rng.Intn(len(alphabet))])
		}
		return b.String()
	}

	cfg := NewConfig()
	seen := make(map[string]bool)
	for i := 0; i < 500; i++ {
		section := randString(sectionAlphabet, 8)
		if ValidateSection(section) != nil {
			continue
		}
		key := randString(keyAlphabet, 8)
		if ValidateKey(key) != nil || seen[section+"\x00"+key] {
			continue
		}
		value := randString(valueAlphabet, 24)
		if ValidateValue(value) != nil {
			continue
		}
		seen[section+"\x00"+key] = true
		if err := cfg.Set(section, key, value); err != nil {
			t.Fatalf("Set(%q, %q, %q) failed: %v", section, key, value, err)
		}
	}

	first, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	parsed, err := Unmarshal(first)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v\ninput:\n%s", err, first)
	}
	if !reflect.DeepEqual(parsed, cfg) {
		t.Fatalf("random round trip mismatch\n got: %+v\nwant: %+v", parsed, cfg)
	}
	second, err := Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("random round trip is not idempotent")
	}
}
