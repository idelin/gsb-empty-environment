package netsec

import (
	"errors"
	"testing"
)

func wantErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got error %v, want %v", err, target)
	}
}

func TestValidatePartName(t *testing.T) {
	valid := []string{
		"/[Content_Types].xml",
		"/_rels/.rels",
		"/word/document.xml",
		"/word/media/image1.png",
		"/word/media/My%20Photo.jpeg",
		"/customXml/itemProps1.xml",
		"/ppt/embeddings/oleObject1.bin",
	}
	for _, name := range valid {
		if _, err := validatePartName(name); err != nil {
			t.Errorf("validatePartName(%q) unexpected error: %v", name, err)
		}
	}

	invalid := []string{
		"word/document.xml",     // 不以 "/" 开头
		"/word//document.xml",   // 空段
		"/word/document.xml/",   // 结尾分隔符
		"/word/../document.xml", // 点段
		"/word/./document.xml",  // 点段
		"/word/document",        // 最后一段无扩展名
		"/word/docu<ment>.xml",  // 保留字符
		"/word/docu|ment.xml",   // 保留字符
		"/word/bad[1].xml",      // 括号仅允许根级 Content_Types
		"/word/trailing.",       // 结尾点且无扩展名
		"/word/enc%2fslash.xml", // 编码后的路径分隔符
		"/word/enc%5Cslash.xml", // 编码后的反斜线
		"/word/%00null.xml",     // 编码 NUL
		"/word/bad%zz.xml",      // 非法十六进制转义
	}
	for _, name := range invalid {
		if _, err := validatePartName(name); err == nil {
			t.Errorf("validatePartName(%q) expected error, got nil", name)
		}
	}
}

func TestDuplicatePartNames(t *testing.T) {
	pkg, err := NewPackage(
		"/word/document.xml",
		"/word/media/image1.png",
		"/word/media/image2.jpeg",
		"/ppt/embeddings/oleObject1.bin",
	)
	if err != nil {
		t.Fatalf("NewPackage: %v", err)
	}

	duplicates := []string{
		"/word/DOCUMENT.XML",             // 大小写变体
		"/word/%64ocument.xml",           // 百分号编码变体（d -> %64）
		"/WORD/media/IMAGE1.PNG",         // 目录大小写变体
		"/word/media/image%31.png",       // 数字编码变体
		"/ppt/embeddings/oleobject1.BIN", // 混合大小写
	}
	for _, name := range duplicates {
		err := pkg.AddPart(name)
		if !errors.Is(err, ErrDuplicatePart) {
			t.Errorf("AddPart(%q) = %v, want ErrDuplicatePart", name, err)
		}
	}

	if got := pkg.PartCount(); got != 4 {
		t.Fatalf("PartCount = %d, want 4 (duplicates must not be registered)", got)
	}

	// 不同名称仍可登记。
	if err := pkg.AddPart("/word/settings.xml"); err != nil {
		t.Fatalf("AddPart distinct name: %v", err)
	}
	if !pkg.HasPart("/WORD/settings.XML") {
		t.Error("HasPart should resolve case-insensitively")
	}
}

func TestResolveTarget_Internal(t *testing.T) {
	pkg, err := NewPackage(
		"/word/document.xml",
		"/word/media/image1.png",
		"/word/embeddings/oleObject1.bin",
		"/customXml/item1.xml",
	)
	if err != nil {
		t.Fatalf("NewPackage: %v", err)
	}

	const rels = "/word/_rels/document.xml.rels"

	tests := []struct {
		name   string
		target string
		mode   string
		want   string
	}{
		{"sibling", "settings.xml", "", "/word/settings.xml"},
		{"media image", "media/image1.png", "", "/word/media/image1.png"},
		{"embeddings", "embeddings/oleObject1.bin", "", "/word/embeddings/oleObject1.bin"},
		{"root relative", "/customXml/item1.xml", "", "/customXml/item1.xml"},
		{"in-package dot segment", "../customXml/item1.xml", "", "/customXml/item1.xml"},
		{"encoded space", "media/ima%67e1.png", "", "/word/media/image1.png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pkg.ResolveTarget(rels, tt.target, tt.mode, false, false)
			if err != nil {
				t.Fatalf("ResolveTarget: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ResolveTarget = %q, want %q", got, tt.want)
			}
		})
	}

	// requirePart：解析结果必须真实存在于包中。
	if _, err := pkg.ResolveTarget(rels, "media/image1.png", "", false, true); err != nil {
		t.Errorf("existing part: %v", err)
	}
	if _, err := pkg.ResolveTarget(rels, "media/missing.png", "", false, true); !errors.Is(err, ErrMissingPart) {
		t.Errorf("missing part: got %v, want ErrMissingPart", err)
	}
}

func TestResolveTarget_EscapeAndEncoding(t *testing.T) {
	pkg, err := NewPackage("/word/document.xml", "/[Content_Types].xml")
	if err != nil {
		t.Fatalf("NewPackage: %v", err)
	}
	const rels = "/word/_rels/document.xml.rels"

	// 越出包根：点段把解析位置推到 "/" 之上。
	escaping := []string{
		"../../../../etc/passwd",
		"../../../../windows/system32/cmd.exe",
		"/../secret.xml",
	}
	for _, target := range escaping {
		if _, err := pkg.ResolveTarget(rels, target, "", false, false); !errors.Is(err, ErrPathEscape) {
			t.Errorf("ResolveTarget(%q) = %v, want ErrPathEscape", target, err)
		}
	}

	// 编码点段：解码后必须同样暴露为 ".."，无法借百分号绕过。
	encoded := []string{
		"%2e%2e/%2e%2e/etc/passwd",
		"%2E%2E/%2E%2E/etc/passwd",
		"..%2f..%2fetc%2fpasswd",
		"media/%2e%2e/%2e%2e/evil.bin",
	}
	for _, target := range encoded {
		_, err := pkg.ResolveTarget(rels, target, "", false, false)
		if !errors.Is(err, ErrPathEscape) && !errors.Is(err, ErrInvalidPartName) {
			t.Errorf("ResolveTarget(%q) = %v, want ErrPathEscape or ErrInvalidPartName", target, err)
		}
	}

	// 反斜线在内部目标中直接拒绝。
	backslashes := []string{
		"..\\..\\etc\\passwd",
		"media\\image1.png",
	}
	for _, target := range backslashes {
		if _, err := pkg.ResolveTarget(rels, target, "", false, false); !errors.Is(err, ErrInvalidPartName) {
			t.Errorf("ResolveTarget(%q) = %v, want ErrInvalidPartName", target, err)
		}
	}

	// 根关系部件的基准目录是 "/"，任何 ".." 都越界。
	if _, err := pkg.ResolveTarget("/_rels/.rels", "../evil.xml", "", false, false); !errors.Is(err, ErrPathEscape) && !errors.Is(err, ErrInvalidPartName) {
		t.Errorf("root rels escape: got %v, want ErrPathEscape or ErrInvalidPartName", err)
	}
}

func TestResolveTarget_External(t *testing.T) {
	pkg, err := NewPackage("/word/document.xml")
	if err != nil {
		t.Fatalf("NewPackage: %v", err)
	}
	const rels = "/word/_rels/document.xml.rels"

	// 默认不允许外部目标。
	if _, err := pkg.ResolveTarget(rels, "https://example.com/payload.bin", "External", false, false); !errors.Is(err, ErrExternalTarget) {
		t.Errorf("external disallowed: got %v, want ErrExternalTarget", err)
	}

	// 显式允许时外部 URI 原样保留。
	kept, err := pkg.ResolveTarget(rels, "https://example.com/payload.bin", "External", true, false)
	if err != nil {
		t.Fatalf("external allowed: %v", err)
	}
	if kept != "https://example.com/payload.bin" {
		t.Fatalf("external target = %q", kept)
	}

	// 即使允许，外部目标也必须是合法绝对 URI。
	for _, bad := range []string{"not a uri", "1no-scheme:x", "../relative.bin"} {
		if _, err := pkg.ResolveTarget(rels, bad, "External", true, false); !errors.Is(err, ErrExternalURI) {
			t.Errorf("ResolveTarget(%q): got %v, want ErrExternalURI", bad, err)
		}
	}
}

func TestSafeOLEPackageName(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"plain", "report.bin", "report.bin"},
		{"windows path", "C:\\Users\\victim\\AppData\\Local\\Temp\\report.bin", "report.bin"},
		{"posix traversal", "../../../../etc/passwd", "passwd"},
		{"unc path", `\\fileserver\share\macro.bin`, "macro.bin"},
		{"encoded traversal", "%2e%2e%2f%2e%2e%2fetc%2fshadow", "shadow"},
		{"double encoded", "%252e%252e%252fpayload.dll", "payload.dll"},
		{"backslash traversal", "..\\..\\windows\\system32\\evil.exe", "evil.exe"},
		{"trailing separators", "/tmp/extract/object1.bin///", "object1.bin"},
		{"spaces allowed", "my object.bin", "my object.bin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SafeOLEPackageName(tt.raw)
			if err != nil {
				t.Fatalf("SafeOLEPackageName: %v", err)
			}
			if got != tt.want {
				t.Fatalf("SafeOLEPackageName = %q, want %q", got, tt.want)
			}
			if containsAnyRune(got, "/\\") {
				t.Fatalf("package name %q must not contain path separators", got)
			}
		})
	}

	unsafe := []string{
		"",
		"../../",
		".hidden.bin",
		"name\x00.bin",
		"bad|name.bin",
		"CON",
		"nul.dll",
		"trailingdot.",
	}
	for _, raw := range unsafe {
		if _, err := SafeOLEPackageName(raw); !errors.Is(err, ErrUnsafeOLEName) {
			t.Errorf("SafeOLEPackageName(%q) = %v, want ErrUnsafeOLEName", raw, err)
		}
	}
}

func containsAnyRune(s, chars string) bool {
	for _, r := range chars {
		if containsRune(s, r) {
			return true
		}
	}
	return false
}

func containsRune(s string, r rune) bool {
	for _, got := range s {
		if got == r {
			return true
		}
	}
	return false
}

// TestEmbeddedImageWorkflow 模拟一次合法嵌入图片的完整导入校验链路。
func TestEmbeddedImageWorkflow(t *testing.T) {
	pkg, err := NewPackage(
		"/[Content_Types].xml",
		"/_rels/.rels",
		"/word/document.xml",
		"/word/_rels/document.xml.rels",
		"/word/media/image1.png",
		"/word/media/photo%20one.jpeg",
		"/word/embeddings/oleObject1.bin",
	)
	if err != nil {
		t.Fatalf("NewPackage: %v", err)
	}

	rels := "/word/_rels/document.xml.rels"

	// 关系指向同包内的嵌入图片，解析后必须能在包中找到。
	resolved, err := pkg.ResolveTarget(rels, "media/image1.png", "", false, true)
	if err != nil {
		t.Fatalf("resolve image: %v", err)
	}
	if resolved != "/word/media/image1.png" {
		t.Fatalf("resolved = %q", resolved)
	}

	// 带编码空格的图片名。
	resolved, err = pkg.ResolveTarget(rels, "media/photo%20one.jpeg", "", false, true)
	if err != nil {
		t.Fatalf("resolve encoded image: %v", err)
	}
	if resolved != "/word/media/photo one.jpeg" {
		t.Fatalf("resolved encoded image = %q", resolved)
	}

	// 嵌入 OLE 对象：原始文件名带穿越路径，但最终包名只保留安全最后一段，
	// 并作为合法部件名的最后一段被包接受。
	oleBase, err := SafeOLEPackageName("C:\\Users\\victim\\AppData\\Local\\Temp\\payload.dll")
	if err != nil {
		t.Fatalf("SafeOLEPackageName: %v", err)
	}
	olePart := "/word/embeddings/" + oleBase
	if err := pkg.AddPart(olePart); err != nil {
		t.Fatalf("AddPart OLE part: %v", err)
	}
	resolved, err = pkg.ResolveTarget(rels, "embeddings/payload.dll", "", false, true)
	if err != nil {
		t.Fatalf("resolve OLE part: %v", err)
	}
	if resolved != olePart {
		t.Fatalf("resolved OLE = %q, want %q", resolved, olePart)
	}
}
