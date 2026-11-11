package policypub_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"example.org/offlinecli/policypub"
)

func secondGenerationPolicy(t *testing.T) policypub.Policy {
	t.Helper()
	return policypub.Policy{
		Rules: []policypub.Rule{
			{
				Name:       "r2",
				Local:      mustPrefix(t, "10.0.0.0/8"),
				Remote:     mustPrefix(t, "198.51.100.0/24"),
				OutboundSA: "ob2",
				InboundSA:  "ib2",
			},
			{
				Name:       "r3",
				Local:      mustPrefix(t, "10.2.0.0/16"),
				Remote:     mustPrefix(t, "203.0.113.0/24"),
				OutboundSA: "ob3",
				InboundSA:  "ib3",
			},
		},
		SAs: []policypub.SA{
			{Handle: "ob2", Direction: policypub.Outbound, Key: keyFor("ob2")},
			{Handle: "ob3", Direction: policypub.Outbound, Key: keyFor("ob3")},
			{Handle: "ib2", Direction: policypub.Inbound, Key: keyFor("ib2")},
			{Handle: "ib3", Direction: policypub.Inbound, Key: keyFor("ib3")},
		},
	}
}

// TestPausableDriverStepOrdering holds every step and checks the driver only
// advances when released, recording events in the documented order.
func TestPausableDriverStepOrdering(t *testing.T) {
	inner := policypub.NewSADB()
	driver := policypub.NewPausable(inner)
	pub := policypub.New(driver)

	steps := []policypub.Step{
		policypub.StepBeforeInstallOut,
		policypub.StepAfterInstallOut,
		policypub.StepBeforeInstallIn,
		policypub.StepAfterInstallIn,
		policypub.StepBeforeSwap,
	}
	for _, step := range steps {
		driver.Hold(step)
	}

	publishErr := make(chan error, 1)
	go func() {
		_, err := pub.Publish(context.Background(), basePolicy(t))
		publishErr <- err
	}()

	wantSequence := []policypub.Step{
		policypub.StepBeforeInstallOut,
		policypub.StepAfterInstallOut,
		policypub.StepBeforeInstallIn,
		policypub.StepAfterInstallIn,
		policypub.StepBeforeSwap,
	}
	for i, step := range wantSequence {
		driver.WaitHeld(step)
		events := driver.Events()
		if len(events) != i+1 {
			t.Fatalf("at step %s event count = %d, want %d", step, len(events), i+1)
		}
		if events[i].Step != step {
			t.Fatalf("event %d = %s, want %s", i, events[i].Step, step)
		}
		if step == policypub.StepBeforeSwap {
			if pub.Active() != nil {
				t.Fatal("swap must not happen while the before-swap gate is held")
			}
		}
		driver.Release(step)
	}

	if err := <-publishErr; err != nil {
		t.Fatalf("publish: %v", err)
	}
	if pub.Active() == nil {
		t.Fatal("active generation missing after release")
	}
}

// TestPausableDriverRollbackSequence injects a failure on the second inbound
// install and asserts LIFO uninstall of every SA installed for the attempt,
// while the first generation remains serviceable throughout.
func TestPausableDriverRollbackSequence(t *testing.T) {
	inner := policypub.NewSADB()
	driver := policypub.NewPausable(inner)
	pub := policypub.New(driver)

	if _, err := pub.Publish(context.Background(), basePolicy(t)); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	driver.Reset()

	injected := errors.New("boom")
	driver.Fail("ib3", injected)

	// Readers keep consuming the first generation while the failed attempt runs.
	stop := make(chan struct{})
	violations := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				violations <- nil
				return
			default:
				d := pub.Decide(policypub.Outbound, mustAddr(t, "10.0.0.7"), mustAddr(t, "192.0.2.7"))
				if d.Action == policypub.ActionEncrypt {
					if d.SAHandle != "ob1" || !inner.Installed("ob1") {
						violations <- fmt.Errorf("bad decision during rollback: %+v", d)
						return
					}
				} else if d.Action != policypub.ActionDrop {
					violations <- fmt.Errorf("impossible action %d", d.Action)
					return
				}
			}
		}
	}()

	_, err := pub.Publish(context.Background(), secondGenerationPolicy(t))
	if !errors.Is(err, injected) {
		t.Fatalf("publish error = %v, want %v", err, injected)
	}
	close(stop)
	if vErr := <-violations; vErr != nil {
		t.Fatal(vErr)
	}

	// Rollback order must be reverse install order: ib2, ob3, ob2.
	var uninstalled []string
	for _, ev := range driver.Events() {
		if ev.Step == policypub.StepUninstall {
			uninstalled = append(uninstalled, ev.Handle)
		}
	}
	wantUninstall := []string{"ib2", "ob3", "ob2"}
	if fmt.Sprint(uninstalled) != fmt.Sprint(wantUninstall) {
		t.Fatalf("rollback uninstall order = %v, want %v", uninstalled, wantUninstall)
	}

	for _, handle := range []string{"ob2", "ob3", "ib2", "ib3"} {
		if inner.Installed(handle) {
			t.Errorf("failed-attempt SA %s must be reclaimed", handle)
		}
	}
	for _, handle := range []string{"ob1", "ib1"} {
		if !inner.Installed(handle) {
			t.Errorf("first-generation SA %s must remain installed", handle)
		}
	}
	if gen := pub.Active().Generation(); gen != 1 {
		t.Fatalf("active generation = %d, want 1", gen)
	}
}

// TestPausableDriverSensitiveFlowsDuringRollback checks each sensitive flow is
// encrypted under an installed SA or dropped at every sampled decision while a
// rollback is in progress.
func TestPausableDriverSensitiveFlowsDuringRollback(t *testing.T) {
	inner := policypub.NewSADB()
	driver := policypub.NewPausable(inner)
	pub := policypub.New(driver)

	if _, err := pub.Publish(context.Background(), basePolicy(t)); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	driver.Reset()

	driver.Hold(policypub.StepUninstall)
	driver.Fail("ib2", errors.New("inbound down"))

	done := make(chan error, 1)
	go func() {
		_, err := pub.Publish(context.Background(), secondGenerationPolicy(t))
		done <- err
	}()

	// Wait until rollback starts uninstalling the first reclaimed SA.
	driver.WaitHeld(policypub.StepUninstall)

	sensitive := []struct {
		dir           policypub.Direction
		src, dst      string
		expectedOldSA string
	}{
		{policypub.Outbound, "10.0.0.7", "192.0.2.7", "ob1"},
		{policypub.Inbound, "192.0.2.7", "10.0.0.7", "ib1"},
	}
	for i := 0; i < 500; i++ {
		for _, flow := range sensitive {
			d := pub.Decide(flow.dir, mustAddr(t, flow.src), mustAddr(t, flow.dst))
			switch d.Action {
			case policypub.ActionEncrypt:
				if d.SAHandle != flow.expectedOldSA || !inner.Installed(d.SAHandle) {
					t.Fatalf("sensitive flow %s->%s got %+v, want installed %s or drop",
						flow.src, flow.dst, d, flow.expectedOldSA)
				}
			case policypub.ActionDrop:
			default:
				t.Fatalf("plaintext-capable action observed: %+v", d)
			}
		}
	}

	driver.Release(policypub.StepUninstall)
	if err := <-done; err == nil {
		t.Fatal("expected the injected failure to surface")
	}
}
