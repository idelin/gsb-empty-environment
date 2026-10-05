// Package calib implements multi-channel clock calibration with integer
// offsets and rational drift rates, plus partial-order event alignment.
//
// Model: a channel clock reading r relates to reference time t by
//
//	r = rho*t + beta
//
// where rho is a rational drift rate and beta is an integer offset.
// A calibration point asserts that at reference time T the channel
// reading lies inside an error interval [Lo, Hi].
package calib

import "math/big"

// Point is a single calibration point: at reference time Ref the channel
// reading is known to lie in [Lo, Hi].
type Point struct {
	ID  string
	Ref *big.Rat
	Lo  *big.Rat
	Hi  *big.Rat
}

// Channel groups the calibration points of one clock channel.
type Channel struct {
	Name   string
	Points []Point
}

// Event is a timestamped observation on a channel.
type Event struct {
	ID      string
	Channel string
	Reading *big.Rat
}

// Input is the full problem instance.
type Input struct {
	Channels []Channel
	Events   []Event
}
