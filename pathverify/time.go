package pathverify

import (
	"crypto/x509"
	"time"
)

// timeValue 包装 time.Time，使 Options 的零值可被识别为“使用当前时间”。
type timeValue struct {
	t  time.Time
	ok bool
}

// AtTime 用指定时刻构造验证时间（测试与离线验证场景使用）。
func AtTime(t time.Time) timeValue {
	return timeValue{t: t.UTC(), ok: true}
}

func (v timeValue) orNow() time.Time {
	if v.ok {
		return v.t
	}
	return time.Now().UTC()
}

func inValidityWindow(c *x509.Certificate, now time.Time) bool {
	nb, na := c.NotBefore.UTC(), c.NotAfter.UTC()
	return (now.Equal(nb) || now.After(nb)) && (now.Equal(na) || now.Before(na))
}
