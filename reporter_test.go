package netsec

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRejectsUnsupportedSchemes(t *testing.T) {
	for _, raw := range []string{
		"http://reports.example.com/",
		"ftp://reports.example.com/",
		"tel:+1-555-0100",
		"//reports.example.com/",
		"reports.example.com",
	} {
		if _, err := ParseIodef(raw); err == nil {
			t.Errorf("ParseIodef(%q) succeeded, want error", raw)
		}
	}
}

func TestMailtoHeaderInjection(t *testing.T) {
	for _, raw := range []string{
		"mailto:admin@example.com?subject=pwned",
		"mailto:admin@example.com?bcc=all@evil.example",
		"mailto:admin@example.com?",
		"mailto:admin@example.com?body=fake",
	} {
		if _, err := ParseIodef(raw); err == nil {
			t.Errorf("ParseIodef(%q) succeeded, want header injection rejected", raw)
		}
	}
}

func TestMailtoAddressConstraints(t *testing.T) {
	for _, raw := range []string{
		"mailto:a@example.com,b@example.com", // 多个地址
		"mailto:Admin <a@example.com>",       // 显示名
		"mailto:a@example.com b@example.com", // 多个地址
		"mailto:a\x01b@example.com",          // 本地部分控制字符
		"mailto:a\rb@example.com",            // 本地部分 CR
		"mailto:a\nb@example.com",            // 本地部分 LF
		"mailto:",                            // 空地址
		"mailto:no-at-sign",                  // 缺少 @
	} {
		if _, err := ParseIodef(raw); err == nil {
			t.Errorf("ParseIodef(%q) succeeded, want error", raw)
		}
	}
	tg, err := ParseIodef("mailto:security@example.com")
	if err != nil {
		t.Fatalf("ParseIodef valid mailto: %v", err)
	}
	if tg.Scheme != SchemeMailto || tg.Address != "security@example.com" {
		t.Errorf("got %+v", tg)
	}
}

func TestHTTPSTargetConstraints(t *testing.T) {
	for _, raw := range []string{
		"https://user@reports.example.com/",      // userinfo
		"https://user:pass@reports.example.com/", // userinfo + 密码
		"https://reports.example.com:8443/",      // 非 443 端口
		"https://reports.example.com:80/",        // 非 443 端口
		"https://:443/",                          // 缺少主机
	} {
		if _, err := ParseIodef(raw); err == nil {
			t.Errorf("ParseIodef(%q) succeeded, want error", raw)
		}
	}
	for _, raw := range []string{
		"https://reports.example.com/",
		"https://reports.example.com:443/iodef",
	} {
		if _, err := ParseIodef(raw); err != nil {
			t.Errorf("ParseIodef(%q) = %v, want success", raw, err)
		}
	}
}

func TestHTTPSInternalAddressRejected(t *testing.T) {
	bad := []string{
		"127.0.0.1", "10.0.0.8", "172.16.5.4", "192.168.1.1",
		"169.254.1.1", "0.0.0.0", "100.64.1.1", "224.0.0.1",
		"::1", "fe80::1", "fc00::1", "ff02::1",
	}
	for _, ip := range bad {
		r := &Reporter{
			Resolve: func(context.Context, string) ([]net.IP, error) {
				return []net.IP{net.ParseIP(ip)}, nil
			},
		}
		sent, err := r.ReportRejected(context.Background(), "victim.example", "id-1",
			[]string{"https://reports.example.com/"})
		if sent != 0 || err == nil {
			t.Errorf("internal address %s: sent=%d err=%v, want rejection", ip, sent, err)
		}
	}

	// 只要有一个地址不是全局单播，整体拒绝。
	r := &Reporter{
		Resolve: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("203.0.113.10"), net.ParseIP("192.168.1.1")}, nil
		},
	}
	if sent, err := r.ReportRejected(context.Background(), "victim.example", "id-1",
		[]string{"https://reports.example.com/"}); sent != 0 || err == nil {
		t.Errorf("mixed addresses: sent=%d err=%v, want rejection", sent, err)
	}
}

// 通过校验的全局单播地址。
const testGlobalIP = "203.0.113.10"

// roundTripFunc 便于在测试中伪造 http.RoundTripper。
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestHTTPSNoRedirect(t *testing.T) {
	var followed atomic.Bool
	var gotBody atomic.Value
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/report":
			b, _ := io.ReadAll(req.Body)
			gotBody.Store(string(b))
			return &http.Response{
				Status:     "302 Found",
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"/landing"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		case "/landing":
			followed.Store(true)
			return &http.Response{
				Status:     "200 OK",
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		}
		return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
	})
	r := &Reporter{
		Resolve: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP(testGlobalIP)}, nil
		},
		Transport: rt,
	}
	sent, err := r.ReportRejected(context.Background(), "victim.example", "acct-rej-123",
		[]string{"https://iodef.example.com/report"})
	if err != nil {
		t.Fatalf("ReportRejected: %v", err)
	}
	if sent != 1 {
		t.Fatalf("sent = %d, want 1", sent)
	}
	if followed.Load() {
		t.Error("client followed redirect to /landing")
	}
	body, _ := gotBody.Load().(string)
	if !strings.Contains(body, "acct-rej-123") {
		t.Errorf("report body missing rejected identifier: %q", body)
	}
	if !strings.Contains(body, "reported-at:") {
		t.Errorf("report body missing timestamp: %q", body)
	}
}

func TestHTTPSDialsValidatedAddress(t *testing.T) {
	ips := []net.IP{net.ParseIP("203.0.113.10"), net.ParseIP("198.51.100.7")}
	var dialed []string
	r := &Reporter{
		Dial: func(_ context.Context, _, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return nil, fmt.Errorf("no network in test")
		},
	}
	tr := r.httpsTransport("iodef.example.com", ips)
	_, err := tr.DialContext(context.Background(), "tcp", "iodef.example.com:443")
	if err == nil {
		t.Fatal("DialContext succeeded, want dial error")
	}
	want := []string{"203.0.113.10:443", "198.51.100.7:443"}
	if !reflect.DeepEqual(dialed, want) {
		t.Errorf("dialed = %v, want validated addresses %v", dialed, want)
	}
	if tr.TLSClientConfig.ServerName != "iodef.example.com" {
		t.Errorf("TLS ServerName = %q", tr.TLSClientConfig.ServerName)
	}
}

func TestMaxTwoIodefRecords(t *testing.T) {
	var mails []string
	r := &Reporter{
		SendMail: func(_ context.Context, to, _, _ string) error {
			mails = append(mails, to)
			return nil
		},
	}
	sent, err := r.ReportRejected(context.Background(), "victim.example", "id-1", []string{
		"mailto:a@example.com",
		"mailto:b@example.com",
		"mailto:c@example.com",
		"mailto:d@example.com",
	})
	if err != nil {
		t.Fatalf("ReportRejected: %v", err)
	}
	if sent != 2 {
		t.Errorf("sent = %d, want 2", sent)
	}
	want := []string{"a@example.com", "b@example.com"}
	if !reflect.DeepEqual(mails, want) {
		t.Errorf("recipients = %v, want %v", mails, want)
	}
}

func TestDailyLimitPerDomain(t *testing.T) {
	now := time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)
	var mails int
	r := &Reporter{
		Now: func() time.Time { return now },
		SendMail: func(context.Context, string, string, string) error {
			mails++
			return nil
		},
	}
	records := []string{"mailto:security@example.com"}

	if sent, _ := r.ReportRejected(context.Background(), "victim.example", "id-1", records); sent != 1 {
		t.Fatalf("first report: sent = %d, want 1", sent)
	}
	// 同一天重复调用不再发送。
	if sent, _ := r.ReportRejected(context.Background(), "victim.example", "id-2", records); sent != 0 {
		t.Fatalf("same-day repeat: sent = %d, want 0", sent)
	}
	if mails != 1 {
		t.Fatalf("mails = %d, want 1", mails)
	}
	// 其他域名不受限。
	if sent, _ := r.ReportRejected(context.Background(), "other.example", "id-3", records); sent != 1 {
		t.Fatalf("other domain: sent = %d, want 1", sent)
	}
	// 次日可再次发送。
	now = now.Add(24 * time.Hour)
	if sent, _ := r.ReportRejected(context.Background(), "victim.example", "id-4", records); sent != 1 {
		t.Fatalf("next day: sent = %d, want 1", sent)
	}
	if mails != 3 {
		t.Fatalf("mails = %d, want 3", mails)
	}
}

func TestInvalidRecordDoesNotConsumeDailyQuota(t *testing.T) {
	var mails int
	r := &Reporter{
		SendMail: func(context.Context, string, string, string) error {
			mails++
			return nil
		},
	}
	if sent, err := r.ReportRejected(context.Background(), "victim.example", "id-1",
		[]string{"http://not-allowed.example/"}); sent != 0 || err == nil {
		t.Fatalf("invalid record: sent=%d err=%v", sent, err)
	}
	if sent, _ := r.ReportRejected(context.Background(), "victim.example", "id-1",
		[]string{"mailto:security@example.com"}); sent != 1 {
		t.Fatalf("after invalid record: sent = %d, want 1", sent)
	}
	if mails != 1 {
		t.Fatalf("mails = %d, want 1", mails)
	}
}

func TestReportBodyContents(t *testing.T) {
	var body string
	r := &Reporter{
		Now: func() time.Time { return time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC) },
		SendMail: func(_ context.Context, _, _, b string) error {
			body = b
			return nil
		},
	}
	_, err := r.ReportRejected(context.Background(), "victim.example", "dns-id-abc\r\ninjected: x",
		[]string{"mailto:security@example.com"})
	if err != nil {
		t.Fatalf("ReportRejected: %v", err)
	}
	if !strings.Contains(body, "dns-id-abc") {
		t.Errorf("body missing rejected identifier: %q", body)
	}
	if !strings.Contains(body, "2026-11-13T09:30:00Z") {
		t.Errorf("body missing report time: %q", body)
	}
	if strings.Contains(body, "injected") {
		t.Errorf("body contains injected field: %q", body)
	}
}
