# fir

Streaming FIR filter with checkpointable state.

- `New(coeffs)` / `NewRational(nums, dens)` — create a filter from floating
  point or rational coefficients.
- `ProcessSample` / `ProcessBlock` — filter samples; any chunking, including
  empty blocks, yields identical output.
- `ScheduleSwitch(at, coeffs)` — switch coefficients at a sample boundary;
  samples already in the history keep participating under the new
  coefficients.
- `Export` / `Restore` — suspend and resume. A restored filter is
  sample-for-sample identical to one that ran continuously. Checkpoints are
  versioned; restoring an incompatible version returns `ErrVersionMismatch`.

The state is a ring buffer of previous input samples plus the absolute
stream position, so per-sample summation order never depends on block
boundaries.
