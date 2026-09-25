package segstore

import "errors"

// FaultPoint identifies an injection point in the commit state machine.
// A fault injected at a point models a process crash at that exact moment:
// the in-memory Store is discarded and the directory is reopened later.
type FaultPoint string

const (
	FaultBeforeBuild    FaultPoint = "before_build"
	FaultAfterBuild     FaultPoint = "after_build"
	FaultBeforeValidate FaultPoint = "before_validate"
	FaultAfterValidate  FaultPoint = "after_validate"
	FaultBeforePublish  FaultPoint = "before_publish"
	FaultAfterPublish   FaultPoint = "after_publish"
	FaultBeforeReclaim  FaultPoint = "before_reclaim"
	FaultAfterReclaim   FaultPoint = "after_reclaim"
)

// ErrInjectedFault is returned by a FaultFunc to simulate a crash.
var ErrInjectedFault = errors.New("segstore: injected fault")

// FaultFunc is invoked at every state-machine transition. Returning a
// non-nil error aborts the current call immediately without changing the
// on-disk journal, so Open can resume afterwards.
type FaultFunc func(FaultPoint) error

func noFault(FaultPoint) error { return nil }
