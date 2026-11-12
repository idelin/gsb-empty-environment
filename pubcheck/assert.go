package pubcheck

import (
	"fmt"
	"testing"
)

// AssertNoRace 运行检查并在发现发布竞争时通过 t.Fatalf 输出具体竞争对与诊断路径。
// 测试宿主可直接调用：pubcheck.AssertNoRace(t, checker)。
func AssertNoRace(t testing.TB, c *Checker) {
	t.Helper()
	races := c.Check()
	if len(races) == 0 {
		return
	}
	var b []byte
	b = append(b, fmt.Sprintf("pubcheck: %d unsafe publication race(s):\n", len(races))...)
	for i, race := range races {
		b = append(b, fmt.Sprintf("race %d: %s\n", i+1, race.Message())...)
		b = append(b, FormatChain(race.Chain)...)
	}
	t.Fatalf("%s", b)
}

// AssertRaceCount 断言检查器恰好报告 want 个竞争，便于负向测试。
func AssertRaceCount(t testing.TB, c *Checker, want int) []Race {
	t.Helper()
	races := c.Check()
	if len(races) != want {
		var b []byte
		b = append(b, fmt.Sprintf("pubcheck: expected %d race(s), got %d:\n", want, len(races))...)
		for i, race := range races {
			b = append(b, fmt.Sprintf("race %d: %s\n", i+1, race.Message())...)
			b = append(b, FormatChain(race.Chain)...)
		}
		t.Fatalf("%s", b)
	}
	return races
}
