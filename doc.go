// Package zerobubble builds and validates split F/B/W pipeline schedules in the
// style of Zero-Bubble (ZB-H1/H2) pipeline parallelism.
//
// Every schedule is a resource-constrained DAG:
//
//   - F(m,s) is the forward pass of microbatch m on stage s.
//   - B(m,s) computes activation gradients, depends on F(m,s) and the
//     downstream B(m,s+1), and immediately unlocks the upstream stage.
//   - W(m,s) computes weight gradients, depends only on local F(m,s) and
//     B(m,s), and never blocks another stage.
//
// A stage executes one task at a time. Every adjacent-stage F or B edge has a
// one-way latency of Config.TComm and is represented by a matched synchronous
// Send/Recv pair in Schedule.Comms.
//
// Activation memory is charged when F starts, reduced from MemF to MemW when B
// completes, and released when W completes. Audit/Schedule.Validate verify that
// all per-stage peaks are <= the configured memory limit, all dependencies are
// satisfied, stage intervals never overlap, P2P ordering is FIFO, and the final
// task/message graph is acyclic.
package zerobubble
