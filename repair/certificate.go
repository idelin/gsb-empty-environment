package repair

import "fmt"

// CheckCertificate independently verifies an infeasibility certificate. It is
// self-contained: it depends only on GF(256) arithmetic and the caller's input,
// not on how the scheduler produced the certificate.
//
// A certificate is valid when the vector has one entry per source dimension,
// every readable shard row has inner product zero with it, and the indicated
// target row has inner product one.
func CheckCertificate(dim int, shards []Shard, targets [][]byte, certificate *Certificate) error {
	if certificate == nil {
		return fmt.Errorf("repair: certificate must not be nil")
	}
	if certificate.TargetIndex < 0 || certificate.TargetIndex >= len(targets) {
		return fmt.Errorf("repair: certificate target index %d out of range [0,%d)",
			certificate.TargetIndex, len(targets))
	}
	if len(certificate.Vector) != dim {
		return fmt.Errorf("repair: certificate vector length %d, want %d",
			len(certificate.Vector), dim)
	}
	for i, target := range targets {
		if len(target) != dim {
			return fmt.Errorf("repair: target %d row has length %d, want %d", i, len(target), dim)
		}
	}
	for i, shard := range shards {
		if len(shard.Row) != dim {
			return fmt.Errorf("repair: shard %d row has length %d, want %d", i, len(shard.Row), dim)
		}
		if got := gfDot(shard.Row, certificate.Vector); got != 0 {
			return fmt.Errorf("repair: shard %d inner product with certificate vector is %d, want 0", i, got)
		}
	}
	target := targets[certificate.TargetIndex]
	if got := gfDot(target, certificate.Vector); got != 1 {
		return fmt.Errorf("repair: target %d inner product with certificate vector is %d, want 1",
			certificate.TargetIndex, got)
	}
	return nil
}
