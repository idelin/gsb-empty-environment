package leasearb

import (
	"io/ioutil"
	"strings"
	"testing"
)

// TestDemoScript 重放演示脚本：时钟前跳、延迟确认、交接并发三个场景。
func TestDemoScript(t *testing.T) {
	data, err := ioutil.ReadFile("scripts/demo.events")
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	r := NewRunner(t0, &sb)
	if err := r.Run(string(data)); err != nil {
		t.Fatalf("replay failed: %v\n--- trace ---\n%s", err, sb.String())
	}
	l, active := r.Arbiter().Snapshot()
	if !active || l.Holder != "grace" || l.Generation != 6 {
		t.Fatalf("unexpected final state: %v active=%v", l, active)
	}
}

func TestScriptExpectFailureIsDetected(t *testing.T) {
	var sb strings.Builder
	r := NewRunner(t0, &sb)
	err := r.Run("acquire alice 10s\nexpect-lease bob 1\n")
	if err == nil {
		t.Fatal("failed expectation should make Run return error")
	}
}
