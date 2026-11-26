// Package example 演示多跳重试控制与稳态分析包的典型用法。
package example

import (
	"context"
	"errors"
	"fmt"
	"time"

	"prescreen.local/ps-9f073c0c88/retry"
	"prescreen.local/ps-9f073c0c88/steady"
)

// 运行时：在一条边上执行带预算、截止期与对冲的请求。
func ExampleEdge() {
	edge := retry.NewEdge(retry.Config{
		MaxAttempts:         4,
		BudgetRatio:         0.2,
		MinRetryConcurrency: 2,
		Window:              5 * time.Second,
		BaseBackoff:         time.Millisecond,
		BackoffMultiplier:   2,
		Jitter:              0.1,
		HedgeDelay:          10 * time.Millisecond,
		MinProcessingTime:   2 * time.Millisecond,
		Breaker: retry.BreakerConfig{
			FailureThreshold:      5,
			OpenCoolDown:          50 * time.Millisecond,
			HalfOpenMaxConcurrent: 1,
		},
	})

	attempt := 0
	res := edge.Do(
		context.Background(),
		"request-123",
		time.Now().Add(100*time.Millisecond), // 传播下来的绝对截止期
		func(ctx context.Context) error {
			attempt++
			if attempt < 3 {
				return errors.New("transient overload")
			}
			return nil
		},
	)
	fmt.Println("success:", res.Success)
	fmt.Println("attempts:", res.Attempts)
	fmt.Println("allowed retries now:", edge.AllowedRetries())
	// Output:
	// success: true
	// attempts: 3
	// allowed retries now: 2
}

// 稳态：4 跳链在无预算时雪崩、20% 预算时收敛。
func ExampleSolve() {
	curve := steady.LinearCurve{Soft: 0.7, Base: 0.05, PAtHard: 0.6}
	rc := steady.EdgeRetry{MaxAttempts: 4, BudgetRatio: 0.2}
	graph := &steady.Graph{
		Nodes: []steady.Node{
			{Name: "A", Capacity: 1000, Curve: curve, IngressRetry: &rc},
			{Name: "B", Capacity: 1000, Curve: curve},
			{Name: "C", Capacity: 1000, Curve: curve},
			{Name: "D", Capacity: 1000, Curve: curve},
		},
		Edges: []steady.Edge{
			{From: "A", To: "B", Fanout: 1, Retry: rc},
			{From: "B", To: "C", Fanout: 1, Retry: rc},
			{From: "C", To: "D", Fanout: 1, Retry: rc},
		},
		Offered: map[string]float64{"A": 700},
	}
	res, _ := steady.Solve(graph, steady.DefaultSolverOptions())
	fmt.Println("converged:", res.Converged)
	for _, name := range res.Order {
		r := res.Nodes[name]
		fmt.Printf("%s physical=%.1f loadRatio=%.2f amp=%.2f\n",
			name, r.PhysicalRate, r.LoadRatio, r.Amplification)
	}
	// Output:
	// converged: true
	// A physical=840.0 loadRatio=0.84 amp=1.20
	// B physical=840.0 loadRatio=0.84 amp=1.20
	// C physical=840.0 loadRatio=0.84 amp=1.20
	// D physical=840.0 loadRatio=0.84 amp=1.20
}
