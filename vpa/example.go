package vpa

// ExamplePair builds the demo automata: two versions of a request/response
// protocol parser over the visibly pushdown alphabet
//
//	open  (call)     begin a request, pushes a session frame S
//	close (return)   end a request, pops S; a stray close on an empty
//	                 stack is tolerated via the empty-stack return rule
//	exec  (internal) do work inside the current request
//
// parser-v1 allows any number of execs per request level; parser-v2 allows
// at most one exec per level and has no transition for a second one
// (implicit trap). They first disagree on "open exec exec close".
func ExamplePair() (a, b *VPA) {
	a = New("parser-v1", 2, 0)
	a.AddSymbol("open", Call).AddSymbol("close", Return).AddSymbol("exec", Internal)
	a.AddFinal(0)
	a.AddCall(0, "open", "S", 1)
	a.AddCall(1, "open", "S", 1)
	a.AddInternal(1, "exec", 1)
	a.AddReturn(1, "close", "S", 0)
	a.AddReturnEmpty(0, "close", 0)

	b = New("parser-v2", 3, 0)
	b.AddSymbol("open", Call).AddSymbol("close", Return).AddSymbol("exec", Internal)
	b.AddFinal(0)
	b.AddCall(0, "open", "S", 1)
	b.AddCall(1, "open", "S", 1)
	b.AddCall(2, "open", "S", 1)
	b.AddInternal(1, "exec", 2) // one exec per level: 1 -> 2, no rule for a second
	b.AddReturn(1, "close", "S", 0)
	b.AddReturn(2, "close", "S", 0)
	b.AddReturnEmpty(0, "close", 0)
	return a, b
}
