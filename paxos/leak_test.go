package paxos

import "runtime"

func countGoroutines() int { return runtime.NumGoroutine() }
