package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"time"
)

// 传输层挂钩：默认走 TCP；测试与单机演示可切换为进程内内存网络（见 memnet.go）。
var (
	listenFn = func(addr string) (net.Listener, error) {
		return net.Listen("tcp", addr)
	}
	dialFn = func(ctx context.Context, addr string) (net.Conn, error) {
		d := net.Dialer{Timeout: 500 * time.Millisecond}
		return d.DialContext(ctx, "tcp", addr)
	}
)

// serve 在空闲端口启动 HTTP 服务并返回实际地址。
func serve(handler http.Handler) (*http.Server, string, error) {
	ln, err := listenFn("127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	return srv, ln.Addr().String(), nil
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func decodeJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// postJSON 发送 JSON POST 并解码响应，使用传入的 ctx 控制超时。
func postJSON(ctx context.Context, client *http.Client, url string, in, out interface{}) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("POST %s: status %d: %s", url, resp.StatusCode, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func defaultClient() *http.Client {
	return &http.Client{
		Timeout: 500 * time.Millisecond,
		Transport: &http.Transport{
			MaxIdleConns:        64,
			MaxIdleConnsPerHost: 8,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialFn(ctx, addr)
			},
		},
	}
}
