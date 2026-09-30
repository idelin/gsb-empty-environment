package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePorts(t *testing.T) {
	cases := []struct {
		in      string
		lo, hi  uint16
		wantErr bool
	}{
		{"any", 0, 65535, false},
		{"80", 80, 80, false},
		{"8000-8080", 8000, 8080, false},
		{"8080-8000", 0, 0, true},
		{"abc", 0, 0, true},
		{"70000", 0, 0, true},
	}
	for _, c := range cases {
		lo, hi, err := parsePorts(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("parsePorts(%q) err=%v，期望错误=%v", c.in, err, c.wantErr)
		}
		if err == nil && (lo != c.lo || hi != c.hi) {
			t.Errorf("parsePorts(%q)=%d-%d，期望 %d-%d", c.in, lo, hi, c.lo, c.hi)
		}
	}
}

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigOK(t *testing.T) {
	path := writeTempConfig(t, `{
	  "rules": [
	    {"name":"web","prefix":"10.0.0.0/8","proto":"tcp","ports":"80","action":"allow"},
	    {"name":"dns","prefix":"2001:db8::/32","proto":["udp","tcp"],"ports":"53-53","action":"deny"},
	    {"name":"all","prefix":"0.0.0.0/0","proto":"any","ports":"any","action":"deny"}
	  ],
	  "sim": {"serviceTicks":2,"linkDelay":3,"bytesPerTick":100,"queueCapacity":4}
	}`)
	rules, cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 3 {
		t.Fatalf("应加载 3 条规则，得到 %d", len(rules))
	}
	if rules[1].Protos != ProtoUDP|ProtoTCP {
		t.Fatalf("协议数组解析错误: %v", rules[1].Protos)
	}
	if rules[1].Family() != "ipv6" || rules[0].Family() != "ipv4" {
		t.Fatal("地址族识别错误")
	}
	if cfg.ServiceTicks != 2 || cfg.QueueCapacity != 4 {
		t.Fatalf("仿真参数解析错误: %+v", cfg)
	}
}

func TestLoadConfigBadPrefix(t *testing.T) {
	path := writeTempConfig(t, `{"rules":[{"prefix":"999.0.0.0/8","proto":"tcp","ports":"80","action":"allow"}]}`)
	if _, _, err := LoadConfig(path); err == nil {
		t.Fatal("非法前缀应报错")
	}
}

func TestLoadConfigBadAction(t *testing.T) {
	path := writeTempConfig(t, `{"rules":[{"prefix":"10.0.0.0/8","proto":"tcp","ports":"80","action":"permit"}]}`)
	if _, _, err := LoadConfig(path); err == nil {
		t.Fatal("非法动作应报错")
	}
}

func TestLoadConfigBadPorts(t *testing.T) {
	path := writeTempConfig(t, `{"rules":[{"prefix":"10.0.0.0/8","proto":"tcp","ports":"9000-8000","action":"allow"}]}`)
	if _, _, err := LoadConfig(path); err == nil {
		t.Fatal("端口下界大于上界应报错")
	}
}
