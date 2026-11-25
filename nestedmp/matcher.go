package nestedmp

// delimEvent 是定界匹配器对单个字节的判定结果。
type delimEvent int

const (
	evNone  delimEvent = iota // 没有完整定界行，字节按正文处理
	evOpen                    // 完整的开放分隔行 "--name\r\n"
	evClose                   // 完整的结束行 "--name--\r\n"
)

// 匹配器内部状态。状态机只做一件事：在行首识别完整的定界行，
// 任何不完整的候选项都会原样回退为正文，因此正文中允许出现任意字节。
const (
	mInit      = iota // 尚未消费任何字节；流首允许直接出现 "--name"
	mScan             // 正文中，等待 CR
	mCR               // 见到 CR，等待 LF；不完整则 CR 属于正文
	mLineStart        // 刚确认 CRLF，位于行首，等待 '-'
	mLead1            // 行首见到第一个 '-'
	mName             // 正在逐字符匹配完整边界名
	mNameFull         // 边界名已完整匹配，等待 CR（开放）或 '-'（结束）
	mOpenCR           // 开放行末尾见到 CR，等待 LF
	mDash1            // 结束行第一个额外 '-'
	mDash2            // 结束行第二个额外 '-'，等待 CR
	mCloseCR          // 结束行末尾见到 CR，等待 LF
)

// delimMatcher 是单层边界的流式匹配器。
//
// 它一次处理一个字节，任何尚未证实为定界行的候选项都保存在 hold 中；
// 候选项失败时整段按正文吐出。hold 的容量上限为 len(name)+8，
// 对应 "CRLF--name--CRLF" 这一最长合法候选项，与输入切块方式无关。
type delimMatcher struct {
	name      string
	state     int
	ncnt      int
	hold      []byte
	lastDelim []byte // 最近识别的完整定界行（含前导 CRLF），reset 后保留
}

func newDelimMatcher(name string) *delimMatcher {
	return &delimMatcher{
		name:  name,
		state: mInit,
		hold:  make([]byte, 0, len(name)+8),
	}
}

// reset 在成功识别一条定界行后调用，后续正文从普通扫描态开始。
func (m *delimMatcher) reset() {
	m.state = mScan
	m.ncnt = 0
	m.hold = m.hold[:0]
}

// rejectCandidate 在候选项证实失败时回退状态，并在内部重放当前字节。
//
// hold 以候选项的前导 CRLF 开头（流首候选除外）。失败时只吐出 "--..."
// 候选部分，前导 CRLF 保留在 hold 中并回到行首态；随后立即按行首规则
// 重放当前字节。任何字节因此都恰好处理一次，与切块方式无关。
func (m *delimMatcher) rejectCandidate(ch byte, onBody func([]byte)) (delimEvent, bool) {
	lead := 0
	if len(m.hold) >= 2 && m.hold[0] == '\r' && m.hold[1] == '\n' {
		lead = 2
	}
	if len(m.hold) > lead {
		onBody(m.hold[lead:])
	}
	m.hold = m.hold[:lead]
	m.ncnt = 0
	if lead > 0 {
		m.state = mLineStart
	} else {
		m.state = mInit
	}
	return m.step(ch, onBody)
}

// step 处理单个字节，且总是消费该字节。
func (m *delimMatcher) step(ch byte, onBody func([]byte)) (delimEvent, bool) {
	switch m.state {
	case mInit:
		switch ch {
		case '-':
			m.hold = append(m.hold, ch)
			m.state = mLead1
		default:
			// 流首不是定界行：非空序言，按正文交给上层判定（上层会拒绝）。
			m.state = mScan
			onBody([]byte{ch})
		}
	case mScan:
		switch ch {
		case '-':
			// 行外的连字号只是普通正文。
			onBody([]byte{ch})
		case '\r':
			m.hold = append(m.hold, ch)
			m.state = mCR
		default:
			onBody([]byte{ch})
		}
	case mCR:
		switch ch {
		case '\n':
			m.hold = append(m.hold, ch)
			m.state = mLineStart
		default:
			return m.rejectCandidate(ch, onBody)
		}
	case mLineStart:
		switch ch {
		case '-':
			m.hold = append(m.hold, ch)
			m.state = mLead1
		default:
			// 非 '-'：行首不是定界行，hold 中的 CRLF 确认为正文，
			// 当前字节回到普通扫描态处理。
			if len(m.hold) > 0 {
				onBody(m.hold)
				m.hold = m.hold[:0]
			}
			m.state = mScan
			return m.step(ch, onBody)
		}
	case mLead1:
		switch ch {
		case '-':
			m.hold = append(m.hold, ch)
			m.ncnt = 0
			m.state = mName
		default:
			return m.rejectCandidate(ch, onBody)
		}
	case mName:
		if m.ncnt < len(m.name) && ch == m.name[m.ncnt] {
			m.hold = append(m.hold, ch)
			m.ncnt++
			if m.ncnt == len(m.name) {
				m.state = mNameFull
			}
		} else {
			return m.rejectCandidate(ch, onBody)
		}
	case mNameFull:
		switch ch {
		case '\r':
			m.hold = append(m.hold, ch)
			m.state = mOpenCR
		case '-':
			m.hold = append(m.hold, ch)
			m.state = mDash1
		default:
			return m.rejectCandidate(ch, onBody)
		}
	case mOpenCR:
		switch ch {
		case '\n':
			span := len(m.hold) + 1 // 包含当前 LF
			m.lastDelim = append(m.lastDelim[:0], m.hold...)
			m.lastDelim = append(m.lastDelim, ch)
			m.reset()
			return evOpen | delimEvent(span<<8), true
		default:
			return m.rejectCandidate(ch, onBody)
		}
	case mDash1:
		switch ch {
		case '-':
			m.hold = append(m.hold, ch)
			m.state = mDash2
		default:
			return m.rejectCandidate(ch, onBody)
		}
	case mDash2:
		switch ch {
		case '\r':
			m.hold = append(m.hold, ch)
			m.state = mCloseCR
		default:
			return m.rejectCandidate(ch, onBody)
		}
	case mCloseCR:
		switch ch {
		case '\n':
			span := len(m.hold) + 1
			m.lastDelim = append(m.lastDelim[:0], m.hold...)
			m.lastDelim = append(m.lastDelim, ch)
			m.reset()
			return evClose | delimEvent(span<<8), true
		default:
			return m.rejectCandidate(ch, onBody)
		}
	}
	return evNone, true
}

// feed 处理一个字节，返回定界事件与该定界行（含紧邻其前的 CRLF；
// 流首定界行无前导 CRLF）的总跨度。span 仅在事件非 evNone 时有效。
func (m *delimMatcher) feed(ch byte, onBody func([]byte)) (event delimEvent, span int) {
	ev, _ := m.step(ch, onBody)
	if ev == evNone {
		return evNone, 0
	}
	return ev & 0xff, int(ev >> 8)
}

// flushEOF 在输入结束时把尚未证实为定界行的候选项交给正文。
func (m *delimMatcher) flushEOF(onBody func([]byte)) {
	if len(m.hold) > 0 {
		onBody(m.hold)
		m.hold = m.hold[:0]
	}
}
