// Package enable 实现 RFC 5161 ENABLE 扩展协商与 RFC 6855 UTF8=ACCEPT
// 邮箱名编码切换，供 IMAP 代理的命令分发层调用。
package enable

import (
	"errors"
	"fmt"
	"strings"
)

// State 为 IMAP 连接状态。
type State int

const (
	StateNotAuthenticated State = iota
	StateAuthenticated
	StateSelected
)

// ErrBadState 表示 ENABLE COMMAND 在当前连接状态下不被受理。
var ErrBadState = errors.New("enable: ENABLE not valid in current state")

// CapUTF8Accept 是 RFC 6855 定义的扩展名。
const CapUTF8Accept = "UTF8=ACCEPT"

// Session 跟踪单条连接的协商状态。
type Session struct {
	state      State
	caps       map[string]bool // 该连接当前 CAPABILITY（大写）
	enabled    map[string]bool
	utf8Accept bool
}

// NewSession 以该连接当前通告的能力集创建会话，初始为未认证状态。
func NewSession(caps []string) *Session {
	s := &Session{
		caps:    make(map[string]bool, len(caps)),
		enabled: make(map[string]bool),
	}
	for _, c := range caps {
		s.caps[strings.ToUpper(c)] = true
	}
	return s
}

// SetState 由命令分发层在认证、选箱、登出等状态迁移时调用。
func (s *Session) SetState(st State) { s.state = st }

// UTF8Accept 报告 UTF8=ACCEPT 是否已启用。
func (s *Session) UTF8Accept() bool { return s.utf8Accept }

// Enable 处理一条 ENABLE 命令的参数，返回应放入 ENABLED
// 无标记应答的能力名列表。仅在已认证且未选邮箱状态受理；
// 不在当前 CAPABILITY 中的名字按 RFC 5161 忽略，不出现在返回值中。
func (s *Session) Enable(names []string) ([]string, error) {
	if s.state != StateAuthenticated {
		return nil, ErrBadState
	}
	var out []string
	for _, n := range names {
		up := strings.ToUpper(n)
		if !s.caps[up] || s.enabled[up] {
			continue
		}
		s.enabled[up] = true
		if up == CapUTF8Accept {
			s.utf8Accept = true
		}
		out = append(out, up)
	}
	return out, nil
}

// CanonicalMailboxName 把线上原始邮箱名解码为规范 UTF-8 名。
// 启用 UTF8=ACCEPT 后按 UTF-8 解释，'&' 不再是 UTF-7 起始符；
// 否则按修改版 UTF-7 解码。
func (s *Session) CanonicalMailboxName(raw string) (string, error) {
	if s.utf8Accept {
		if !isValidUTF8(raw) {
			return "", fmt.Errorf("enable: mailbox name is not valid UTF-8")
		}
		return raw, nil
	}
	return DecodeUTF7(raw)
}
