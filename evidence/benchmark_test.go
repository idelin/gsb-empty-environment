package evidence_test

import (
	"fmt"
	"testing"

	"example.org/domainreissue/q012/evidence"
)

func BenchmarkEvaluate18Facts(b *testing.B) {
	message := []byte("benchmark message")
	relay := newTestNode(b, "relay-a", true)
	engine := engineFor(relay)
	facts := make([]*evidence.Fact, evidence.DefaultMaxFacts)
	for i := range facts {
		facts[i] = mustSign(b, relay, fmt.Sprintf("f%02d", i), "method", message, true)
	}
	rule := parseRule(b,
		"f00 and f01 and f02 and f03 and f04 and f05 and f06 and f07 and f08 and "+
			"f09 and f10 and f11 and f12 and f13 and f14 and f15 and f16 and f17")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		decision, err := engine.Evaluate(rule, message, facts)
		if err != nil {
			b.Fatal(err)
		}
		if !decision.Release || len(decision.MinimalSets[0]) != evidence.DefaultMaxFacts {
			b.Fatal("unexpected decision")
		}
	}
}
