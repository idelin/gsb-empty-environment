package netsec

import "errors"

// ErrUnknownTask is returned when no running or historical training task
// matches the supplied ID.
var ErrUnknownTask = errors.New("netsec: unknown training task")

// ErrTaskExists is returned when a new task or rebuild reuses an ID that is
// already registered.
var ErrTaskExists = errors.New("netsec: training task already exists")
