// Package taskpkg provides stateful predictive packet-loss concealment for
// planar float64 stereo PCM.
//
// Real samples drive an autocorrelation/Levinson-Durbin predictor. Lost
// samples are synthesized from a stable all-pole predictor plus either
// periodic prediction residual or energy-matched random residual. Analysis is
// never updated from synthesized samples. A resumed stream is linearly
// crossfaded over a configurable number of real samples without changing the
// host's sample count.
package taskpkg
