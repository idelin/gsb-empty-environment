package netsec_test

import (
	"errors"
	"sync"
	"testing"

	netsec "example.org/netsec/q-003"
)

const (
	sourceAlpha netsec.Source = 0
	sourceBeta  netsec.Source = 1
)

func mustRequest(t *testing.T, inputs map[netsec.Source]int) *netsec.Request {
	t.Helper()
	request, err := netsec.NewRequest(inputs)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	return request
}

func authorize(
	t *testing.T,
	registrar *netsec.Registrar,
	request *netsec.Request,
	node *netsec.DeclassifyNode,
	sources ...netsec.Source,
) netsec.Authorization {
	t.Helper()
	auth, err := registrar.Authorize(request, node, netsec.NewLabel(sources...))
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	return auth
}

func countEvents(events []netsec.Event, kind netsec.EventKind) int {
	count := 0
	for _, event := range events {
		if event.Kind == kind {
			count++
		}
	}
	return count
}

// A declassification inside a nested secret conditional must not erase the
// outer control dependency. The constant is explicitly low, and the
// authorization even covers both branch sources, so only retaining pc can
// keep the output private.
func TestNestedConditionalLeakIsRejected(t *testing.T) {
	request := mustRequest(t, map[netsec.Source]int{sourceAlpha: 1, sourceBeta: 1})
	registrar := netsec.NewRegistrar()
	executor := netsec.NewExecutor(registrar, nil)

	innerLeak := netsec.Declassify(netsec.Const(1))
	auth := authorize(t, registrar, request, innerLeak, sourceAlpha, sourceBeta)
	leaking := netsec.If(
		netsec.Secret(sourceAlpha),
		netsec.If(
			netsec.Secret(sourceBeta),
			netsec.Output(innerLeak.WithAuthorization(auth)),
			netsec.Const(0),
		),
		netsec.Const(0),
	)

	result, err := executor.Execute(request, leaking)
	if !errors.Is(err, netsec.ErrSecretOutput) {
		t.Fatalf("Execute error = %v, want ErrSecretOutput", err)
	}
	if len(result.Outputs) != 0 {
		t.Fatalf("failed execution exposed %d partial outputs", len(result.Outputs))
	}
	if registrar.Consumed(auth) {
		t.Fatal("failed execution consumed the authorization")
	}

	events := executor.Audit().Events()
	if got := countEvents(events, netsec.EventOutputPublished); got != 0 {
		t.Fatalf("published output events = %d, want 0", got)
	}
	if got := countEvents(events, netsec.EventOutputDenied); got != 1 {
		t.Fatalf("denied output events = %d, want 1", got)
	}
}

// The same control dependency must survive a branch merge: binding the nested
// conditional to a local and outputting it outside the conditional is still a
// leak of both conditions.
func TestNestedConditionalLeakAfterMergeIsRejected(t *testing.T) {
	request := mustRequest(t, map[netsec.Source]int{sourceAlpha: 1, sourceBeta: 1})
	registrar := netsec.NewRegistrar()
	executor := netsec.NewExecutor(registrar, nil)

	site := netsec.Declassify(netsec.Const(1))
	auth := authorize(t, registrar, request, site, sourceAlpha, sourceBeta)
	merged := netsec.If(
		netsec.Secret(sourceAlpha),
		netsec.If(
			netsec.Secret(sourceBeta),
			site.WithAuthorization(auth),
			netsec.Const(0),
		),
		netsec.Const(0),
	)
	expr := netsec.Let("leaked", merged, netsec.Output(netsec.Var("leaked")))

	result, err := executor.Execute(request, expr)
	if !errors.Is(err, netsec.ErrSecretOutput) {
		t.Fatalf("Execute error = %v, want ErrSecretOutput", err)
	}
	if len(result.Outputs) != 0 {
		t.Fatalf("failed execution exposed %d partial outputs", len(result.Outputs))
	}
	if registrar.Consumed(auth) {
		t.Fatal("failed execution consumed the authorization")
	}
}

// Declassification is a set difference, not a blanket "make public" switch:
// sources outside the authorization remain in the label and block output.
func TestDeclassificationRemovesOnlyAllowedSources(t *testing.T) {
	request := mustRequest(t, map[netsec.Source]int{sourceAlpha: 1, sourceBeta: 2})
	registrar := netsec.NewRegistrar()
	executor := netsec.NewExecutor(registrar, nil)

	site := netsec.Declassify(netsec.Add(netsec.Secret(sourceAlpha), netsec.Secret(sourceBeta)))
	auth := authorize(t, registrar, request, site, sourceAlpha)

	result, err := executor.Execute(request, netsec.Output(site.WithAuthorization(auth)))
	if !errors.Is(err, netsec.ErrSecretOutput) {
		t.Fatalf("Execute error = %v, want ErrSecretOutput", err)
	}
	if len(result.Outputs) != 0 {
		t.Fatalf("partially declassified value exposed %d outputs", len(result.Outputs))
	}
	if registrar.Consumed(auth) {
		t.Fatal("failed execution consumed the authorization")
	}

	var rolledBack *netsec.Event
	for _, event := range executor.Audit().Events() {
		if event.Kind == netsec.EventDeclassificationRolledBack {
			copied := event
			rolledBack = &copied
		}
	}
	if rolledBack == nil {
		t.Fatal("missing rolled-back declassification event")
	}
	if rolledBack.Allowed != netsec.NewLabel(sourceAlpha) {
		t.Fatalf("allowed label = %s, want %s", rolledBack.Allowed, netsec.NewLabel(sourceAlpha))
	}
	if rolledBack.Remaining != netsec.NewLabel(sourceBeta) {
		t.Fatalf("remaining label = %s, want %s", rolledBack.Remaining, netsec.NewLabel(sourceBeta))
	}
}

func TestAuthorizationCannotBeReused(t *testing.T) {
	t.Run("twice in one execution", func(t *testing.T) {
		request := mustRequest(t, map[netsec.Source]int{sourceAlpha: 7})
		registrar := netsec.NewRegistrar()
		executor := netsec.NewExecutor(registrar, nil)

		site := netsec.Declassify(netsec.Secret(sourceAlpha))
		auth := authorize(t, registrar, request, site, sourceAlpha)
		authorized := site.WithAuthorization(auth)
		expr := netsec.Output(netsec.Add(authorized, authorized))

		result, err := executor.Execute(request, expr)
		if !errors.Is(err, netsec.ErrAuthorizationReuse) {
			t.Fatalf("Execute error = %v, want ErrAuthorizationReuse", err)
		}
		if len(result.Outputs) != 0 {
			t.Fatalf("failed execution exposed %d partial outputs", len(result.Outputs))
		}
		if registrar.Consumed(auth) {
			t.Fatal("rejected execution consumed the authorization")
		}
		if got := countEvents(executor.Audit().Events(), netsec.EventAuthorizationReuse); got != 1 {
			t.Fatalf("authorization reuse audit events = %d, want 1", got)
		}
	})

	t.Run("after a successful execution", func(t *testing.T) {
		request := mustRequest(t, map[netsec.Source]int{sourceAlpha: 7})
		registrar := netsec.NewRegistrar()
		executor := netsec.NewExecutor(registrar, nil)

		site := netsec.Declassify(netsec.Secret(sourceAlpha))
		auth := authorize(t, registrar, request, site, sourceAlpha)
		expr := netsec.Output(site.WithAuthorization(auth))

		result, err := executor.Execute(request, expr)
		if err != nil {
			t.Fatalf("first Execute: %v", err)
		}
		if len(result.Outputs) != 1 || result.Outputs[0].Value != 7 {
			t.Fatalf("first outputs = %+v, want [7]", result.Outputs)
		}
		if !registrar.Consumed(auth) {
			t.Fatal("successful execution did not consume the authorization")
		}

		result, err = executor.Execute(request, expr)
		if !errors.Is(err, netsec.ErrAuthorizationReuse) {
			t.Fatalf("second Execute error = %v, want ErrAuthorizationReuse", err)
		}
		if len(result.Outputs) != 0 {
			t.Fatalf("reuse exposed %d partial outputs", len(result.Outputs))
		}
	})

	t.Run("does not consume earlier authorizations", func(t *testing.T) {
		request := mustRequest(t, map[netsec.Source]int{sourceAlpha: 4, sourceBeta: 6})
		registrar := netsec.NewRegistrar()
		executor := netsec.NewExecutor(registrar, nil)

		firstSite := netsec.Declassify(netsec.Secret(sourceAlpha))
		firstAuth := authorize(t, registrar, request, firstSite, sourceAlpha)
		secondSite := netsec.Declassify(netsec.Secret(sourceBeta))
		secondAuth := authorize(t, registrar, request, secondSite, sourceBeta)

		if _, err := executor.Execute(request, netsec.Output(secondSite.WithAuthorization(secondAuth))); err != nil {
			t.Fatalf("consume second authorization: %v", err)
		}
		expr := netsec.Output(netsec.Add(
			firstSite.WithAuthorization(firstAuth),
			secondSite.WithAuthorization(secondAuth),
		))
		result, err := executor.Execute(request, expr)
		if !errors.Is(err, netsec.ErrAuthorizationReuse) {
			t.Fatalf("Execute error = %v, want ErrAuthorizationReuse", err)
		}
		if len(result.Outputs) != 0 {
			t.Fatalf("failed execution exposed %d partial outputs", len(result.Outputs))
		}
		if registrar.Consumed(firstAuth) {
			t.Fatal("failed execution consumed an earlier authorization")
		}
		if !registrar.Consumed(secondAuth) {
			t.Fatal("previously committed authorization lost its consumed state")
		}

		result, err = executor.Execute(request, netsec.Output(firstSite.WithAuthorization(firstAuth)))
		if err != nil {
			t.Fatalf("retry first authorization: %v", err)
		}
		if len(result.Outputs) != 1 || result.Outputs[0].Value != 4 {
			t.Fatalf("retry outputs = %+v, want [4]", result.Outputs)
		}
	})

	t.Run("across requests", func(t *testing.T) {
		requestOne := mustRequest(t, map[netsec.Source]int{sourceAlpha: 11})
		requestTwo := mustRequest(t, map[netsec.Source]int{sourceAlpha: 22})
		registrar := netsec.NewRegistrar()
		executor := netsec.NewExecutor(registrar, nil)

		site := netsec.Declassify(netsec.Secret(sourceAlpha))
		auth := authorize(t, registrar, requestOne, site, sourceAlpha)
		expr := netsec.Output(site.WithAuthorization(auth))

		result, err := executor.Execute(requestTwo, expr)
		if !errors.Is(err, netsec.ErrAuthorizationMismatch) {
			t.Fatalf("cross-request Execute error = %v, want ErrAuthorizationMismatch", err)
		}
		if len(result.Outputs) != 0 {
			t.Fatalf("cross-request attempt exposed %d partial outputs", len(result.Outputs))
		}
		if registrar.Consumed(auth) {
			t.Fatal("cross-request rejection consumed the authorization")
		}

		result, err = executor.Execute(requestOne, expr)
		if err != nil {
			t.Fatalf("authorized request Execute: %v", err)
		}
		if len(result.Outputs) != 1 || result.Outputs[0].Value != 11 {
			t.Fatalf("authorized outputs = %+v, want [11]", result.Outputs)
		}
	})

	t.Run("on a different node", func(t *testing.T) {
		request := mustRequest(t, map[netsec.Source]int{sourceAlpha: 9})
		registrar := netsec.NewRegistrar()
		executor := netsec.NewExecutor(registrar, nil)

		site := netsec.Declassify(netsec.Secret(sourceAlpha))
		auth := authorize(t, registrar, request, site, sourceAlpha)
		otherSite := netsec.Declassify(netsec.Secret(sourceAlpha))

		result, err := executor.Execute(request, netsec.Output(otherSite.WithAuthorization(auth)))
		if !errors.Is(err, netsec.ErrAuthorizationMismatch) {
			t.Fatalf("other-node Execute error = %v, want ErrAuthorizationMismatch", err)
		}
		if len(result.Outputs) != 0 {
			t.Fatalf("other-node attempt exposed %d partial outputs", len(result.Outputs))
		}
		if registrar.Consumed(auth) {
			t.Fatal("other-node rejection consumed the authorization")
		}
	})
}

// wrappedAuthorization embeds a real capability. Even if Go promotes the
// unexported marker method and this value satisfies the Authorization
// interface, the executor requires its own concrete registrar record and must
// reject the wrapper.
type wrappedAuthorization struct {
	netsec.Authorization
}

func TestWrappedAuthorizationIsNotAValidCapability(t *testing.T) {
	request := mustRequest(t, map[netsec.Source]int{sourceAlpha: 5})
	registrar := netsec.NewRegistrar()
	executor := netsec.NewExecutor(registrar, nil)

	site := netsec.Declassify(netsec.Secret(sourceAlpha))
	realAuth := authorize(t, registrar, request, site, sourceAlpha)
	wrapped := wrappedAuthorization{Authorization: realAuth}

	result, err := executor.Execute(request, netsec.Output(site.WithAuthorization(wrapped)))
	if !errors.Is(err, netsec.ErrMissingAuthorization) {
		t.Fatalf("wrapped authorization Execute error = %v, want ErrMissingAuthorization", err)
	}
	if len(result.Outputs) != 0 {
		t.Fatalf("wrapped authorization exposed %d partial outputs", len(result.Outputs))
	}
	if registrar.Consumed(realAuth) {
		t.Fatal("wrapped authorization consumed the real capability")
	}

	result, err = executor.Execute(request, netsec.Output(site.WithAuthorization(realAuth)))
	if err != nil {
		t.Fatalf("real authorization Execute: %v", err)
	}
	if len(result.Outputs) != 1 || result.Outputs[0].Value != 5 {
		t.Fatalf("real outputs = %+v, want [5]", result.Outputs)
	}
}

// Embedding a real expression promotes its unexported marker method, but the
// executor's type switch still rejects the wrapper rather than treating it as
// an operator with unknown label semantics.
type wrappedExpr struct {
	netsec.Expr
}

func TestWrappedExpressionIsRejected(t *testing.T) {
	request := mustRequest(t, map[netsec.Source]int{sourceAlpha: 3})
	executor := netsec.NewExecutor(netsec.NewRegistrar(), nil)

	result, err := executor.Execute(request, netsec.Output(wrappedExpr{Expr: netsec.Secret(sourceAlpha)}))
	if !errors.Is(err, netsec.ErrInvalidExpression) {
		t.Fatalf("Execute error = %v, want ErrInvalidExpression", err)
	}
	if len(result.Outputs) != 0 {
		t.Fatalf("wrapped expression exposed %d partial outputs", len(result.Outputs))
	}
}

// An authorization is only meaningful to the registrar that issued it. A
// second registrar cannot manufacture a capability for the first executor.
func TestForeignRegistrarAuthorizationIsRejected(t *testing.T) {
	request := mustRequest(t, map[netsec.Source]int{sourceAlpha: 13})
	trusted := netsec.NewRegistrar()
	foreign := netsec.NewRegistrar()
	executor := netsec.NewExecutor(trusted, nil)

	site := netsec.Declassify(netsec.Secret(sourceAlpha))
	foreignAuth := authorize(t, foreign, request, site, sourceAlpha)

	result, err := executor.Execute(request, netsec.Output(site.WithAuthorization(foreignAuth)))
	if !errors.Is(err, netsec.ErrMissingAuthorization) {
		t.Fatalf("foreign authorization Execute error = %v, want ErrMissingAuthorization", err)
	}
	if len(result.Outputs) != 0 {
		t.Fatalf("foreign authorization exposed %d partial outputs", len(result.Outputs))
	}
	if trusted.Consumed(foreignAuth) || foreign.Consumed(foreignAuth) {
		t.Fatal("foreign authorization was consumed")
	}
}

// A failure after a declassification must roll back both the pending public
// output and the authorization consumption. The same capability is then
// usable by a repaired expression.
func TestFailureRollsBackOutputsAndAuthorization(t *testing.T) {
	request := mustRequest(t, map[netsec.Source]int{sourceAlpha: 41})
	registrar := netsec.NewRegistrar()
	executor := netsec.NewExecutor(registrar, nil)

	site := netsec.Declassify(netsec.Secret(sourceAlpha))
	auth := authorize(t, registrar, request, site, sourceAlpha)

	bad := netsec.Let(
		"declassified",
		site.WithAuthorization(auth),
		netsec.Let(
			"alreadyBuffered",
			netsec.Output(netsec.Const(1)),
			netsec.Output(netsec.Secret(sourceAlpha)),
		),
	)
	result, err := executor.Execute(request, bad)
	if !errors.Is(err, netsec.ErrSecretOutput) {
		t.Fatalf("failed Execute error = %v, want ErrSecretOutput", err)
	}
	if len(result.Outputs) != 0 {
		t.Fatalf("failed execution exposed %d partial outputs", len(result.Outputs))
	}
	if registrar.Consumed(auth) {
		t.Fatal("failed execution consumed the authorization")
	}

	events := executor.Audit().Events()
	if got := countEvents(events, netsec.EventOutputPublished); got != 0 {
		t.Fatalf("published output events after failure = %d, want 0", got)
	}
	if got := countEvents(events, netsec.EventDeclassificationRolledBack); got != 1 {
		t.Fatalf("rolled-back declassification events = %d, want 1", got)
	}

	good := netsec.Output(site.WithAuthorization(auth))
	result, err = executor.Execute(request, good)
	if err != nil {
		t.Fatalf("repaired Execute: %v", err)
	}
	if len(result.Outputs) != 1 || result.Outputs[0].Value != 41 || !result.Outputs[0].Label.IsEmpty() {
		t.Fatalf("repaired outputs = %+v, want one unlabelled 41", result.Outputs)
	}
	if !registrar.Consumed(auth) {
		t.Fatal("successful repaired execution did not consume the authorization")
	}

	events = executor.Audit().Events()
	if got := countEvents(events, netsec.EventOutputPublished); got != 1 {
		t.Fatalf("committed output events = %d, want 1", got)
	}
}

// Declassification at a fixed site reaches an empty label, and applying
// declassification again at the same fixed point leaves the label unchanged.
// Arithmetic over low values must not reintroduce a dependency.
func TestNormalFixedPointDeclassification(t *testing.T) {
	request := mustRequest(t, map[netsec.Source]int{sourceAlpha: 41})
	registrar := netsec.NewRegistrar()
	executor := netsec.NewExecutor(registrar, nil)

	innerSite := netsec.Declassify(netsec.Secret(sourceAlpha))
	innerAuth := authorize(t, registrar, request, innerSite, sourceAlpha)

	outerSite := netsec.Declassify(innerSite.WithAuthorization(innerAuth))
	outerAuth := authorize(t, registrar, request, outerSite, sourceAlpha)

	expr := netsec.Output(netsec.Add(
		outerSite.WithAuthorization(outerAuth),
		netsec.Const(1),
	))
	result, err := executor.Execute(request, expr)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Outputs) != 1 {
		t.Fatalf("outputs = %+v, want exactly one", result.Outputs)
	}
	if got := result.Outputs[0].Value; got != 42 {
		t.Fatalf("output value = %d, want 42", got)
	}
	if !result.Outputs[0].Label.IsEmpty() {
		t.Fatalf("public output label = %s, want empty", result.Outputs[0].Label)
	}
	if !registrar.Consumed(innerAuth) || !registrar.Consumed(outerAuth) {
		t.Fatal("fixed-point declassification did not consume both authorizations")
	}

	events := executor.Audit().Events()
	if got := countEvents(events, netsec.EventDeclassification); got != 2 {
		t.Fatalf("committed declassification events = %d, want 2", got)
	}
	if got := countEvents(events, netsec.EventOutputPublished); got != 1 {
		t.Fatalf("output events = %d, want 1", got)
	}
}

// Concurrent executions of the same request and capability are serialized by
// the request. Exactly one transaction can commit the one-time authorization;
// every loser returns an error with no outputs.
func TestConcurrentSameRequestAuthorizationIsAtomic(t *testing.T) {
	request := mustRequest(t, map[netsec.Source]int{sourceAlpha: 77})
	registrar := netsec.NewRegistrar()
	executor := netsec.NewExecutor(registrar, nil)

	site := netsec.Declassify(netsec.Secret(sourceAlpha))
	auth := authorize(t, registrar, request, site, sourceAlpha)
	expr := netsec.Output(site.WithAuthorization(auth))

	const workers = 16
	start := make(chan struct{})
	type outcome struct {
		result netsec.Result
		err    error
	}
	outcomes := make(chan outcome, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			result, err := executor.Execute(request, expr)
			outcomes <- outcome{result: result, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)

	successes := 0
	reuses := 0
	published := 0
	for outcome := range outcomes {
		switch {
		case outcome.err == nil:
			successes++
			published += len(outcome.result.Outputs)
		case errors.Is(outcome.err, netsec.ErrAuthorizationReuse):
			reuses++
			if len(outcome.result.Outputs) != 0 {
				t.Fatalf("reuse returned %d partial outputs", len(outcome.result.Outputs))
			}
		default:
			t.Fatalf("unexpected concurrent error: %v", outcome.err)
		}
	}
	if successes != 1 || reuses != workers-1 {
		t.Fatalf("successes/reuses = %d/%d, want 1/%d", successes, reuses, workers-1)
	}
	if published != 1 {
		t.Fatalf("published outputs = %d, want 1", published)
	}
	if !registrar.Consumed(auth) {
		t.Fatal("winning transaction did not consume the authorization")
	}
	if got := countEvents(executor.Audit().Events(), netsec.EventOutputPublished); got != 1 {
		t.Fatalf("audit output events = %d, want 1", got)
	}
}

// Independent concurrent requests each commit their own request-bound
// capability. No request can observe another request's output or partial
// state.
func TestConcurrentIndependentRequests(t *testing.T) {
	registrar := netsec.NewRegistrar()
	executor := netsec.NewExecutor(registrar, nil)

	const workers = 12
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		value := i + 100
		request := mustRequest(t, map[netsec.Source]int{sourceAlpha: value})
		site := netsec.Declassify(netsec.Secret(sourceAlpha))
		auth := authorize(t, registrar, request, site, sourceAlpha)
		expr := netsec.Output(site.WithAuthorization(auth))

		go func() {
			defer wg.Done()
			<-start
			result, err := executor.Execute(request, expr)
			if err != nil {
				errs <- err
				return
			}
			if len(result.Outputs) != 1 || result.Outputs[0].Value != value || !result.Outputs[0].Label.IsEmpty() {
				errs <- errors.New("unexpected output for independent request")
				return
			}
			if !registrar.Consumed(auth) {
				errs <- errors.New("independent request did not consume its authorization")
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
