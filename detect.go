package sqlifinger

import "strings"

// 默认危险指纹（折叠后的词类子序列）。匹配采用子串语义：
// 参数值折叠结果中只要包含任一指纹即判为注入。
var defaultFingerprints = []string{
	// 恒真式：AND/OR 1=1、'a'='a'、a=b、`a`=`b`
	"&1o1", "&sos", "&vov", "&non",
	// 括号包裹的串比较：') OR ('1'='1
	"&(sos",
	// 嵌套括号恒真式：AND((1)=(1))
	"1)o(1",
	// UNION 注入：UNION SELECT / UNION ALL SELECT / UNION(SELECT
	"Uk", "U(",
	// 时间盲注 / 报错注入函数：AND SLEEP(、AND UPDATEXML( 等
	"&f(",
	// AND (SELECT ...)、AND ((SELECT ...
	"&(k", "&((k",
	// 堆叠查询：'; DROP / '; SELECT
	";k",
}

// Detection 描述一次命中。
type Detection struct {
	Context     Context // 命中时使用的起始上下文
	Fingerprint string  // 该上下文下参数值的完整词类序列
	Pattern     string  // 命中的危险指纹
}

// Detector 按可配置的危险指纹集做 SQL 注入词法检测。
// 零值不可用，请用 DefaultDetector 或 NewDetector 构造。
type Detector struct {
	fingerprints []string
}

// DefaultFingerprints 返回内置危险指纹集的副本。
func DefaultFingerprints() []string {
	return append([]string(nil), defaultFingerprints...)
}

// DefaultDetector 返回使用内置指纹集的检测器。
func DefaultDetector() *Detector {
	return NewDetector(defaultFingerprints...)
}

// NewDetector 用自定义指纹集构造检测器（完全替换默认集）。
func NewDetector(fingerprints ...string) *Detector {
	return &Detector{fingerprints: append([]string(nil), fingerprints...)}
}

// Add 追加自定义指纹。
func (d *Detector) Add(patterns ...string) {
	d.fingerprints = append(d.fingerprints, patterns...)
}

// Fingerprints 返回当前指纹集的副本。
func (d *Detector) Fingerprints() []string {
	return append([]string(nil), d.fingerprints...)
}

// Fingerprint 计算参数值在指定起始上下文下的词类指纹。
func (d *Detector) Fingerprint(input string, ctx Context) string {
	return Fold(Tokenize(input, ctx))
}

// Match 在指定起始上下文下检测，返回命中的指纹模式。
func (d *Detector) Match(input string, ctx Context) (string, bool) {
	fp := d.Fingerprint(input, ctx)
	for _, p := range d.fingerprints {
		if p != "" && strings.Contains(fp, p) {
			return p, true
		}
	}
	return "", false
}

// Detect 依次按不在引号内、单引号内、双引号内三种起始上下文
// 切词并匹配，任一上下文命中即判为 SQL 注入。
func (d *Detector) Detect(input string) (Detection, bool) {
	for _, ctx := range []Context{ContextUnquoted, ContextSingleQuote, ContextDoubleQuote} {
		if pattern, ok := d.Match(input, ctx); ok {
			return Detection{
				Context:     ctx,
				Fingerprint: d.Fingerprint(input, ctx),
				Pattern:     pattern,
			}, true
		}
	}
	return Detection{}, false
}
