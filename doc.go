// Package taskpkg provides uniform partitioned convolution for real-time
// audio hosts.
//
// Convolver convolves a single stream with an impulse response using
// uniform block FFT partitioning and overlap-add reconstruction, and
// supports crossfaded impulse-response updates at block boundaries with
// tail preservation. Matrix generalizes this to multi-channel matrix
// convolution where input and output channel counts may differ.
package taskpkg
