// Package taskpkg provides a streaming phase vocoder that performs
// time-scale modification of mono float64 audio without changing pitch.
//
// The caller supplies the FFT length and analysis hop; the synthesis hop is
// not fixed but derived, frame by frame, from a positive rational stretch
// ratio through an exact integer accumulator. For every analysis frame the
// bin-wise phase difference has the expected linear phase advance removed,
// is wrapped onto [-pi, pi), and the residual yields an instantaneous
// frequency that advances the synthesis phase. Magnitudes are kept, so
// spectral content (and therefore pitch) is preserved while timeline length
// changes. Overlap-add output is normalized sample by sample by the
// accumulated squared analysis/synthesis window.
//
// The stretch ratio may change while the stream is running, but a change
// always takes effect at an analysis-frame boundary, never inside a frame.
// Output depends only on the sequence of input samples, so splitting a
// stream across Write calls of arbitrary sizes produces identical output.
//
// This implementation targets quasi-stationary, tonal material such as
// speech and sustained notes. Transient preservation is explicitly out of
// scope: attacks and other impulsive events may be smeared.
package taskpkg
