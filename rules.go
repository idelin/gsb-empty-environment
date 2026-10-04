package logasm

import "regexp"

// Rules 描述单来源单 epoch 的拼接规则，运行期可热换。
type Rules struct {
	// Start 匹配“起始行”：命中即关闭上一条记录并开启新记录。
	Start *regexp.Regexp
	// ContinuationIndent 为 true 时，以空格/制表符开头的缩进行
	// 视为上一条记录的续行（如异常堆栈）。
	ContinuationIndent bool
	// MaxRecordBytes 限制单条记录正文字节数，0 表示不限。
	// 超限记录被截断并置 Truncated 标记，丢弃字节计入 DroppedBytes。
	MaxRecordBytes int
}

func (r Rules) isStart(line string) bool {
	return r.Start != nil && r.Start.MatchString(line)
}

func isIndented(line string) bool {
	return len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
}
