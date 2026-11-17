// Package netsec implements finite Boolean interpolation for bounded
// conjunctive normal form inputs. It supports a security analysis service
// that needs to distill, from an unreachable vulnerability path, a condition
// blocking every similar path.
//
// Given two CNFs a and b over a shared vocabulary of at most MaxShared (12)
// variables, Interpolate either:
//
//   - returns an interpolant I mentioning only shared variables such that
//     a implies I and I contradicts b, when a and b are jointly
//     unsatisfiable; or
//   - returns a Witness satisfying both CNFs when their conjunction is
//     satisfiable, in which case no safety proof exists and none is
//     fabricated.
//
// The interpolant is built by enumerating the full shared-variable
// assignment space (at most 2^12 assignments) and is then independently
// re-verified with a separate SAT engine in Verify, which checks both
// required properties on every enumerated assignment.
package netsec
