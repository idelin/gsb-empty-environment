package netsec

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// MaxIodefPerReport 是单次上报最多使用的 iodef 记录条数。
const MaxIodefPerReport = 2

// Reporter 在 CAA 拒签流程中按 iodef 记录向域名所有人发送报告。
// 依赖均通过函数字段注入，便于测试与审计。
type Reporter struct {
	// SendMail 发送邮件报告，参数为收件人、主题、正文。
	SendMail func(ctx context.Context, to, subject, body string) error
	// Resolve 解析主机名，返回全部地址；缺省使用系统解析器。
	Resolve func(ctx context.Context, host string) ([]net.IP, error)
	// Dial 建立 TCP 连接；缺省使用带超时的 net.Dialer。
	// 调用方传入的地址永远是已校验的全局单播地址。
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// TLSConfig 为 https 上报的 TLS 配置；ServerName 缺省取目标主机名。
	TLSConfig *tls.Config
	// Transport 为 https 上报使用的 RoundTripper；缺省为只连接
	// 已校验地址的 http.Transport。主要用于测试。
	Transport http.RoundTripper
	// Now 返回当前时间，用于每日限频与报告时间戳；缺省为 time.Now。
	Now func() time.Time

	mu   sync.Mutex
	sent map[string]string // domain -> 已发送日期 (UTC, 2006-01-02)
}

func (r *Reporter) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Reporter) alreadySent(domain string) bool {
	day := r.now().UTC().Format("2006-01-02")
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sent != nil && r.sent[domain] == day
}

func (r *Reporter) markSent(domain string) {
	day := r.now().UTC().Format("2006-01-02")
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sent == nil {
		r.sent = make(map[string]string)
	}
	r.sent[domain] = day
}

// ReportRejected 在域名 domain 因 CAA 被拒签后按 iodef 记录发送报告。
// rejectedIdentifier 仅为该域名本次被拒的标识符；报告正文只包含它与时间，
// 不包含订单中的其他标识符。单个域名每天最多发送一次，每次最多取前两条
// iodef 记录。返回成功发送的条数；无效记录被跳过并汇入返回错误。
func (r *Reporter) ReportRejected(ctx context.Context, domain, rejectedIdentifier string, iodef []string) (int, error) {
	if r.alreadySent(domain) {
		return 0, nil
	}
	if len(iodef) > MaxIodefPerReport {
		iodef = iodef[:MaxIodefPerReport]
	}

	body := fmt.Sprintf("CAA rejection report\r\nrejected-identifier: %s\r\nreported-at: %s\r\n",
		sanitizeField(rejectedIdentifier), r.now().UTC().Format(time.RFC3339))

	sent := 0
	var errs []error
	for i, raw := range iodef {
		t, err := ParseIodef(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("iodef record %d: %w", i, err))
			continue
		}
		var serr error
		switch t.Scheme {
		case SchemeMailto:
			serr = r.sendMail(ctx, t.Address, "CAA rejection report for "+domain, body)
		case SchemeHTTPS:
			serr = r.sendHTTPS(ctx, t, body)
		}
		if serr != nil {
			errs = append(errs, fmt.Errorf("iodef record %d: %w", i, serr))
			continue
		}
		sent++
	}
	if sent > 0 {
		r.markSent(domain)
	}
	return sent, errors.Join(errs...)
}

// sanitizeField 在第一个控制字符处截断标识符，防止报告正文被注入额外字段。
func sanitizeField(s string) string {
	if i := strings.IndexFunc(s, func(r rune) bool {
		return r < 0x20 || r == 0x7f
	}); i >= 0 {
		return s[:i]
	}
	return s
}

func (r *Reporter) sendMail(ctx context.Context, to, subject, body string) error {
	if r.SendMail == nil {
		return errors.New("iodef: no mail sender configured")
	}
	return r.SendMail(ctx, to, subject, body)
}

// sendHTTPS 解析目标主机，要求全部地址均为全局单播，并用已校验的
// 地址建立连接，不跟随重定向。
func (r *Reporter) sendHTTPS(ctx context.Context, t Target, body string) error {
	host := t.URL.Hostname()
	resolve := r.Resolve
	if resolve == nil {
		resolve = func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip", host)
		}
	}
	ips, err := resolve(ctx, host)
	if err != nil {
		return fmt.Errorf("iodef: resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("iodef: no addresses for %s", host)
	}
	for _, ip := range ips {
		if !IsGlobalUnicast(ip) {
			return fmt.Errorf("iodef: address %s for %s is not global unicast", ip, host)
		}
	}

	transport := r.Transport
	if transport == nil {
		tr := r.httpsTransport(host, ips)
		defer tr.CloseIdleConnections()
		transport = tr
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL.String(), strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("iodef: https report to %s: %w", host, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("iodef: https report to %s: status %s", host, resp.Status)
	}
	return nil
}

// httpsTransport 构造只连接已校验地址（端口 443）的 Transport。
func (r *Reporter) httpsTransport(host string, ips []net.IP) *http.Transport {
	tlsConf := &tls.Config{MinVersion: tls.VersionTLS12}
	if r.TLSConfig != nil {
		tlsConf = r.TLSConfig.Clone()
	}
	if tlsConf.ServerName == "" {
		tlsConf.ServerName = host
	}
	dial := r.Dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	}
	return &http.Transport{
		TLSClientConfig: tlsConf,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var lastErr error
			for _, ip := range ips {
				conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), "443"))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
}
