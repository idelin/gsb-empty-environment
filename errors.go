package netsec

import "fmt"

// AlertCode 是 TLS 警报描述符(RFC 8446 附录 B)。
type AlertCode uint8

const (
	AlertUnexpectedMessage AlertCode = 10
	AlertBadCertificate    AlertCode = 42
	AlertIllegalParameter  AlertCode = 47
	AlertDecodeError       AlertCode = 50
	AlertInternalError     AlertCode = 80
)

// AlertError 表示一次应当中止握手的 TLS 警报。
type AlertError struct {
	Code AlertCode
	Msg  string
}

func (e *AlertError) Error() string {
	return fmt.Sprintf("tls: alert(%d): %s", e.Code, e.Msg)
}

func alert(code AlertCode, format string, args ...any) *AlertError {
	return &AlertError{Code: code, Msg: fmt.Sprintf(format, args...)}
}
