package timeparse

import "errors"

// 包内可被 errors.Is 识别的哨兵错误。具体错误会用 fmt.Errorf %w 包裹，
// 携带触发问题的片段或时刻细节。
var (
	// ErrInvalidRules 站点规则不合法（如缺时区、回看窗口非正数）。
	ErrInvalidRules = errors.New("timeparse: invalid rules")
	// ErrNoDate 文本中没有可识别的日期表达式。
	ErrNoDate = errors.New("timeparse: no date expression found")
	// ErrMultipleDateExpressions 文本中出现多个互斥的日期片段。
	ErrMultipleDateExpressions = errors.New("timeparse: multiple date expressions")
	// ErrInvalidDate 日历日期不合法（如非闰年的 2 月 29 日）。
	ErrInvalidDate = errors.New("timeparse: invalid calendar date")
	// ErrOutOfLookback 缺年份日期的所有候选年份都落在回看窗口之外。
	ErrOutOfLookback = errors.New("timeparse: candidate outside lookback window")
	// ErrNonexistentTime 本地时钟不存在该时刻（春令时跳时产生的间隙）。
	ErrNonexistentTime = errors.New("timeparse: nonexistent local time (DST gap)")
)
