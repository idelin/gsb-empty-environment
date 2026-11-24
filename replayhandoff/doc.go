// Package replayhandoff transfers an authenticated stream's replay window to a
// receiver during session-key rotation.
//
// Wire sequence numbers retain only the low b bits. Signed checkpoints bound
// the possible high bits, while per-node logs retain the strict observation
// order seen by each gateway. Receiver state is partitioned by persistent
// device identity and key generation. A frame is admitted only when every
// compatible full-sequence assignment is inside the current window and has not
// already been admitted. When compatible assignments disagree, the decision
// includes both complete assignments and asks the host for another checkpoint.
package replayhandoff
