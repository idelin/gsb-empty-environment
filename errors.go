package netsec

import "errors"

var (
	// ErrInvalidPlan reports a plan that cannot encode the required final
	// metadata or whose public result bound overflows.
	ErrInvalidPlan = errors.New("netsec: invalid output plan")

	// ErrInvalidSchedule reports a non-positive interval or an overflowing
	// schedule.
	ErrInvalidSchedule = errors.New("netsec: invalid schedule")

	// ErrInvalidKey reports an unsupported AES key length.
	ErrInvalidKey = errors.New("netsec: invalid key")

	// ErrRoundReuse reports a (key, round) pair that was already claimed.
	ErrRoundReuse = errors.New("netsec: key and round already used")

	// ErrTooLarge reports a submission beyond the public bound or the available
	// encrypted frame capacity.
	ErrTooLarge = errors.New("netsec: secret result too large")

	// ErrFinished reports a write after the task has reported completion.
	ErrFinished = errors.New("netsec: task already finished")

	// ErrAlreadyFinished reports a second completion report.
	ErrAlreadyFinished = errors.New("netsec: completion already reported")

	// ErrLate reports completion at or after the public deadline, or completion
	// reported after the final frame was emitted.
	ErrLate = errors.New("netsec: task missed the output deadline")

	// ErrEmitted reports an operation that would add another frame after all
	// planned slots have been emitted.
	ErrEmitted = errors.New("netsec: all planned frames already emitted")

	// ErrFrameCount reports a frame sequence that does not match the public
	// number of slots.
	ErrFrameCount = errors.New("netsec: frame count does not match plan")

	// ErrFrameLayout reports a frame with the wrong slot, round, or size.
	ErrFrameLayout = errors.New("netsec: invalid frame layout")

	// ErrAuthentication reports a failed GCM authentication. It intentionally
	// does not distinguish tampering from use of the wrong key or AAD.
	ErrAuthentication = errors.New("netsec: frame authentication failed")

	// ErrMalformedMetadata reports impossible or inconsistent plaintext
	// metadata after successful authentication.
	ErrMalformedMetadata = errors.New("netsec: malformed final metadata")
)
