// Package prioritylb computes normalized traffic shares for endpoint pools
// arranged in Envoy-style priorities (P0..Pn).
//
// The computation combines three independent mechanisms:
//
//   - Envoy priority load assignment: per-priority healthy and degraded
//     availability is scaled by an overprovisioning factor and capped at 100%.
//     P0 is filled first with healthy capacity; any residual load is absorbed
//     by degraded capacity and then by lower priorities. When total effective
//     capacity is below 100% every priority is scaled up proportionally.
//   - Global panic mode: when the raw (pre-overprovisioning) healthy fraction
//     of every non-empty priority is below the panic threshold, health
//     filtering is ignored and traffic is split in proportion to the raw
//     endpoint count of every priority.
//   - Slow start ramp: endpoints whose age is within [0, W) have their
//     effective weight multiplied by max(m, (age/W)^(1/A)), so a freshly
//     started cold endpoint always keeps at least the floor m.
//
// All output probabilities are normalized to sum to exactly 1.0 (up to a final
// floating-point residual correction on the largest element).
package prioritylb
