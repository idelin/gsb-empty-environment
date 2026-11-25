package permcompile

import "strconv"

// 导出的哨兵错误，便于宿主用 errors.Is 分类处理。
var (
	ErrExpired         = errExpired
	ErrNoWorlds        = errNoWorlds
	ErrUnknownGate     = errUnknownGate
	ErrGateSettled     = errGateSettled
	ErrCannotCancel    = errCannotCancel
	ErrBadFallback     = errBadFallback
	ErrUnknownOption   = errUnknownOption
	ErrReplay          = errReplay
	ErrBindSession     = errBindSession
	ErrBindDigest      = errBindDigest
	ErrUnauthenticated = errUnauthenticated
)

func (k EventKind) String() string {
	switch k {
	case EventDecision:
		return "decision"
	case EventFallback:
		return "fallback"
	case EventCancel:
		return "cancel"
	default:
		return "unknown"
	}
}

func (v Verdict) String() string {
	switch v {
	case VerdictAllow:
		return "allow"
	case VerdictDeny:
		return "deny"
	case VerdictWait:
		return "wait"
	default:
		return "unknown"
	}
}

func u64(v uint64) string { return strconv.FormatUint(v, 10) }

func (s nodeState) String() string {
	switch s {
	case stPending:
		return "pending"
	case stActive:
		return "active"
	case stCompleted:
		return "completed"
	case stCanceled:
		return "canceled"
	default:
		return "unknown"
	}
}
