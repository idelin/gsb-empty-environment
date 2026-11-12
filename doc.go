// Package taskpkg implements a 3D chain inverse kinematics solver for Go
// animation hosts.
//
// The main entry point is SolveChain, a constrained FABRIK solver:
//
//   - bone lengths, a fixed root, a tip target and per-joint local cone
//     limits ([JointLimit]) are supplied through [ChainRequest];
//   - each forward/backward position pass re-projects every bone onto its
//     length and clips its direction into the joint cone;
//   - pole-axis degeneracies are resolved with a fixed reference axis so
//     results stay deterministic (for example preventing knee hyperextension);
//   - outputs are solved joint positions plus local quaternions, with the
//     recomputed tip residual, reachability and convergence flags.
//
// Unreachable targets return the minimum-residual full-extension candidate.
// When the iteration budget is spent without reaching tolerance the best
// candidate is returned with Converged set to false. SolveTwoBone provides
// the closed-form law-of-cosines solution for the common two-bone case.
package taskpkg
