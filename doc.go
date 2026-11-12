// Package taskpkg implements a differentiable RGBA color pipeline for a Go
// compositing host. Pipeline graphs can combine RGB matrices, piecewise power
// transfer functions, alpha premultiplication, and premultiplied over
// compositing while propagating the full output Jacobian.
package taskpkg
