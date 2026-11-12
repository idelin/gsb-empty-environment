package netsec

import (
	"bytes"
	"net"
	"testing"
	"time"
)

// step 是脚本服务器的单步动作。
type step struct {
	wait  time.Duration // 写入前等待，模拟分段到达
	data  string        // 原样写入的字节
	close bool          // 写完后关闭连接
}

// startScriptServer 返回一个拨号函数。受沙箱限制无法绑定真实 TCP 端口，
// 这里用 net.Pipe 提供等价的、按脚本逐连接发送字节的进程内“服务器”。
func startScriptServer(t *testing.T, scripts ...[]step) func() net.Conn {
	t.Helper()
	idx := 0
	var dial func() net.Conn
	dial = func() net.Conn {
		client, server := net.Pipe()
		script := []step{}
		if idx < len(scripts) {
			script = scripts[idx]
		}
		idx++
		t.Cleanup(func() { client.Close() })
		go runScript(server, script)
		return client
	}
	return dial
}

func runScript(conn net.Conn, script []step) {
	defer conn.Close()
	buf := new(bytes.Buffer)
	flush := func() {
		if buf.Len() > 0 {
			// net.Pipe 的写会阻塞到对端读，整段写出确保脚本字节原样送达。
			_, _ = conn.Write(buf.Bytes())
			buf.Reset()
		}
	}
	for _, s := range script {
		if s.wait > 0 {
			flush()
			time.Sleep(s.wait)
		}
		if s.data != "" {
			buf.WriteString(s.data)
		}
		if s.close {
			flush()
			_ = conn.Close()
			return
		}
	}
	flush()
}

func dialScript(t *testing.T, dial func() net.Conn) net.Conn {
	t.Helper()
	return dial()
}
