package netsec

import "testing"

func TestAdjudicate(t *testing.T) {
	body := []byte("GET /admin HTTP/1.1\r\nHost: internal\r\n\r\n") // 超长数据

	tests := []struct {
		name    string
		headers []string
		frames  [][]byte
		allowed bool
	}{
		{
			name:    "任务场景：两个不同的 content-length 加超长数据",
			headers: []string{"5", "10"},
			frames:  [][]byte{body},
			allowed: false,
		},
		{
			name:    "content-length 小于数据帧总和",
			headers: []string{"5"},
			frames:  [][]byte{[]byte("0123456789")},
			allowed: false,
		},
		{
			name:    "content-length 大于数据帧总和",
			headers: []string{"10"},
			frames:  [][]byte{[]byte("01234")},
			allowed: false,
		},
		{
			name:    "单个头且长度相符，放行",
			headers: []string{"10"},
			frames:  [][]byte{[]byte("0123"), []byte("456789")},
			allowed: true,
		},
		{
			name:    "重复但一致的 content-length，放行",
			headers: []string{"10", "10"},
			frames:  [][]byte{[]byte("0123456789")},
			allowed: true,
		},
		{
			name:    "逗号合并的重复头不一致，拒绝",
			headers: []string{"5, 10"},
			frames:  [][]byte{[]byte("0123456789")},
			allowed: false,
		},
		{
			name:    "非法的 content-length 值，拒绝",
			headers: []string{"abc"},
			frames:  [][]byte{[]byte("01234")},
			allowed: false,
		},
		{
			name:    "负的 content-length 值，拒绝",
			headers: []string{"-1"},
			frames:  nil,
			allowed: false,
		},
		{
			name:    "无 content-length 头，放行",
			headers: nil,
			frames:  [][]byte{[]byte("01234")},
			allowed: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verdict := Adjudicate(test.headers, test.frames...)
			if verdict.Allowed != test.allowed {
				t.Fatalf("Allowed = %v, 期望 %v（原因：%s）", verdict.Allowed, test.allowed, verdict.Reason)
			}
			t.Logf("裁决：allowed=%v，原因：%s", verdict.Allowed, verdict.Reason)
		})
	}
}
