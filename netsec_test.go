package netsec

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
)

func readerOf(parts ...string) *bufio.Reader {
	return bufio.NewReader(strings.NewReader(strings.Join(parts, "")))
}

var testRules = Rules{
	AllowedExtensions: []string{"fileinto", "reject", "vacation"},
	MaxRedirects:      2,
}

// 攻击一：脚本名里夹带 CRLF（以及 TAB），必须拒绝。
func TestScriptNameCRLFRejected(t *testing.T) {
	cases := []string{
		"evil\r\nDELETESCRIPT \"x\"",
		"evil\nscript",
		"evil\tscript",
		"a\x00b",
		strings.Repeat("a", MaxScriptNameBytes+1),
	}
	for _, bad := range cases {
		if err := ValidateScriptName(bad); err == nil {
			t.Fatalf("expected rejection for name %q", bad)
		}
	}

	// 协议层：名称引号内出现裸 CR 直接判定为畸形命令。
	r := readerOf("PUTSCRIPT \"evil\r\nDELETESCRIPT\" {5+}\r\nhello\r\n")
	if _, err := ReadPutScript(r, testRules); !errors.Is(err, ErrProtocol) {
		t.Fatalf("got %v, want ErrProtocol", err)
	}

	// 合法名称边界：恰好 128 字节、含多字节 UTF-8。
	okName := strings.Repeat("a", MaxScriptNameBytes-6) + "中文" // 122 字节 + 6 字节
	if err := ValidateScriptName(okName); err != nil {
		t.Fatalf("valid name rejected: %v", err)
	}
	if err := ValidateScriptName(""); !errors.Is(err, ErrNameEmpty) {
		t.Fatalf("got %v, want ErrNameEmpty", err)
	}
}

// 攻击二：字面量结束后紧跟 DELETESCRIPT，偷渡字节不得被解释为下一条命令。
func TestTrailingDeleteScriptRejected(t *testing.T) {
	body := "# sieve\r\nkeep;\r\n"
	wire := "PUTSCRIPT \"s1\" {" + itoa(len(body)) + "+}\r\n" + body +
		"\r\nDELETESCRIPT \"victim\"\r\n"
	r := readerOf(wire)
	_, err := ReadPutScript(r, testRules)
	if !errors.Is(err, ErrTrailingData) {
		t.Fatalf("got %v, want ErrTrailingData", err)
	}
	// 偷渡内容仍留在读取器中，由调用方关闭连接，绝不分派为新命令。
	rest, _ := r.ReadString('\n')
	if !strings.HasPrefix(rest, "DELETESCRIPT") {
		t.Fatalf("trailing command should remain unparsed, got %q", rest)
	}

	// 正常命令读取后无残留。
	good := "PUTSCRIPT \"s1\" {" + itoa(len(body)) + "}\r\n" + body + "\r\n"
	parsed, err := ReadPutScript(readerOf(good), testRules)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(parsed.Body) != body {
		t.Fatalf("body mismatch: %q != %q", parsed.Body, body)
	}
	if r := readerOf(good); r.Buffered() != 0 {
		t.Fatal("fresh reader should be empty")
	}
}

// 攻击三：require 站点未开放的扩展（可能用于执行外部程序），拒绝保存。
func TestDisallowedExtensionRejected(t *testing.T) {
	body := []byte("require [\"fileinto\", \"vnd.external.program\"];\r\nkeep;\r\n")
	if err := ValidateScript(body, testRules); !errors.Is(err, ErrExtensionNotAllowed) {
		t.Fatalf("got %v, want ErrExtensionNotAllowed", err)
	}

	// 白名单内的扩展应通过，且大小写不敏感。
	ok := []byte("require [\"FILEINTO\", \"reject\"];\r\nkeep;\r\n")
	if err := ValidateScript(ok, testRules); err != nil {
		t.Fatalf("allowed extension rejected: %v", err)
	}

	// 空白名单下任何 require 都被拒绝（失败关闭）。
	if err := ValidateScript([]byte("require \"fileinto\";\n"), Rules{}); !errors.Is(err, ErrExtensionNotAllowed) {
		t.Fatalf("got %v, want ErrExtensionNotAllowed", err)
	}
}

// 攻击四：require 不是第一条命令，拒绝。
func TestRequireMustBeFirst(t *testing.T) {
	body := []byte("# comment\r\nkeep;\r\nrequire \"fileinto\";\r\n")
	if err := ValidateScript(body, testRules); !errors.Is(err, ErrRequireNotFirst) {
		t.Fatalf("got %v, want ErrRequireNotFirst", err)
	}

	// 出现在其它命令块内部同样拒绝。
	nested := []byte("if true {\r\n  require \"fileinto\";\r\n}\r\n")
	if err := ValidateScript(nested, testRules); !errors.Is(err, ErrRequireNotFirst) {
		t.Fatalf("got %v, want ErrRequireNotFirst", err)
	}

	// require 作为首条命令（前面只有注释和空白）合法。
	first := []byte("/* header */\r\nrequire \"fileinto\";\r\nkeep;\r\n")
	if err := ValidateScript(first, testRules); err != nil {
		t.Fatalf("leading require rejected: %v", err)
	}
}

// redirect 计数：字符串内容不计入，超过上限拒绝。
func TestRedirectLimit(t *testing.T) {
	within := []byte(`redirect "a@example.com";
if header :contains "subject" "redirect" {
  redirect "b@example.com";
}`)
	if err := ValidateScript(within, Rules{MaxRedirects: 2}); err != nil {
		t.Fatalf("two redirects within limit rejected: %v", err)
	}
	if err := ValidateScript(within, Rules{MaxRedirects: 1}); !errors.Is(err, ErrRedirectLimit) {
		t.Fatalf("got %v, want ErrRedirectLimit", err)
	}
}

// 完整入口：任何校验失败都不得保存，也不得覆盖已存在的同名脚本。
func TestHandlePutScriptNoOverwriteOnFailure(t *testing.T) {
	store := NewMemoryStore()
	original := []byte("keep;\r\n")
	if err := store.Put("s1", original); err != nil {
		t.Fatal(err)
	}

	evilBody := "require [\"vnd.external.program\"];\r\n"
	wire := "PUTSCRIPT \"s1\" {" + itoa(len(evilBody)) + "+}\r\n" + evilBody + "\r\n"
	if _, err := HandlePutScript(readerOf(wire), store, testRules); !errors.Is(err, ErrExtensionNotAllowed) {
		t.Fatalf("got %v, want ErrExtensionNotAllowed", err)
	}

	got, ok, err := store.Get("s1")
	if err != nil || !ok {
		t.Fatalf("script missing after failed upload: ok=%v err=%v", ok, err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("existing script overwritten: %q", got)
	}

	// 偷渡 DELETESCRIPT 的请求也不得产生任何写入。
	trailingBody := "keep;" // 结束符 CRLF 独立于字面量内容
	trailing := "PUTSCRIPT \"s1\" {" + itoa(len(trailingBody)) + "+}\r\n" + trailingBody +
		"\r\nDELETESCRIPT \"s1\"\r\n"
	if _, err := HandlePutScript(readerOf(trailing), store, testRules); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("got %v, want ErrTrailingData", err)
	}
	got, _, _ = store.Get("s1")
	if !bytes.Equal(got, original) {
		t.Fatalf("script changed after protocol error: %q", got)
	}

	// 合法上传成功覆盖。
	goodBody := "require \"fileinto\";\r\nkeep;\r\n"
	good := "PUTSCRIPT \"s1\" {" + itoa(len(goodBody)) + "}\r\n" + goodBody + "\r\n"
	parsed, err := HandlePutScript(readerOf(good), store, testRules)
	if err != nil {
		t.Fatalf("valid upload failed: %v", err)
	}
	if parsed.Name != "s1" || string(parsed.Body) != goodBody {
		t.Fatalf("unexpected parsed result: %+v", parsed)
	}
	got, _, _ = store.Get("s1")
	if string(got) != goodBody {
		t.Fatalf("store not updated: %q", got)
	}
}

// 字面量必须精确读取声明的 n 字节，声明过长导致 EOF 时拒绝。
func TestTruncatedLiteralRejected(t *testing.T) {
	wire := "PUTSCRIPT \"s1\" {99}\r\nshort\r\n"
	if _, err := ReadPutScript(readerOf(wire), testRules); !errors.Is(err, ErrTruncatedBody) {
		t.Fatalf("got %v, want ErrTruncatedBody", err)
	}

	// 多行字符串与点填充可被正确词法分析，不被误判为命令。
	body := []byte("require \"reject\";\nreject text:\r\n.Dot line\r\n..stuffed\r\n.\r\n;\n")
	if err := ValidateScript(body, Rules{AllowedExtensions: []string{"reject"}}); err != nil {
		t.Fatalf("multiline body rejected: %v", err)
	}

	// 非 UTF-8 脚本体拒绝。
	if err := ValidateScript([]byte{0xff, 0xfe, 0x00}, testRules); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("got %v, want ErrInvalidUTF8", err)
	}
}

// 引号形式的脚本体同样受尾部数据与名称规则约束。
func TestQuotedBody(t *testing.T) {
	wire := "PUTSCRIPT \"s2\" \"keep;\"\r\n"
	parsed, err := ReadPutScript(readerOf(wire), testRules)
	if err != nil {
		t.Fatalf("quoted body rejected: %v", err)
	}
	if string(parsed.Body) != "keep;" {
		t.Fatalf("body = %q", parsed.Body)
	}

	bad := "PUTSCRIPT \"s2\" \"keep;\" EXTRA\r\n"
	if _, err := ReadPutScript(readerOf(bad), testRules); !errors.Is(err, ErrProtocol) {
		t.Fatalf("got %v, want ErrProtocol", err)
	}
}

// 长度声明超过站点上限直接拒绝，且不尝试分配巨量内存。
func TestOversizedLiteralRejected(t *testing.T) {
	wire := "PUTSCRIPT \"s1\" {999999999}\r\nx\r\n"
	rules := Rules{MaxBodyBytes: 16}
	if _, err := ReadPutScript(readerOf(wire), rules); !errors.Is(err, ErrLiteralLength) {
		t.Fatalf("got %v, want ErrLiteralLength", err)
	}
}

// require 参数结构非法（空列表、缺分号）按语法错误失败关闭。
func TestRequireMalformed(t *testing.T) {
	cases := [][]byte{
		[]byte("require [];\n"),
		[]byte("require \"fileinto\"\n"),
		[]byte("require ;\n"),
		[]byte("require [\"fileinto\"]\n"),
	}
	for _, b := range cases {
		if err := ValidateScript(b, testRules); !errors.Is(err, ErrSieveSyntax) {
			t.Fatalf("body %q got %v, want ErrSieveSyntax", b, err)
		}
	}
}

// 名称中的引号在协议层必须以转义形式出现，解码后仍走名称校验。
func TestNameQuoteEscaping(t *testing.T) {
	wire := "PUTSCRIPT \"a\\\"b\" \"keep;\"\r\n"
	parsed, err := ReadPutScript(readerOf(wire), testRules)
	if err != nil {
		t.Fatalf("escaped quote rejected: %v", err)
	}
	if parsed.Name != `a"b` {
		t.Fatalf("name decoded as %q", parsed.Name)
	}

	// 未转义的引号提前结束名称，导致后续语法不匹配。
	bad := "PUTSCRIPT \"a\"b\" \"keep;\"\r\n"
	if _, err := ReadPutScript(readerOf(bad), testRules); !errors.Is(err, ErrProtocol) {
		t.Fatalf("got %v, want ErrProtocol", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
