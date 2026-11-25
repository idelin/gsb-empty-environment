package ipp

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
)

type BatchItem struct {
	Context     []byte
	DomainLabel []byte
	Generators  Generators
	Commitment  Point
	Proof       *Proof
}

func VerifyBatch(ad Adapter, items []BatchItem) error {
	if len(items) == 0 {
		return fmt.Errorf("%w: no proofs", ErrBatchEmpty)
	}
	for i, item := range items {
		if err := verify(ad, item.DomainLabel, item.Context, item.Generators, item.Commitment, item.Proof); err != nil {
			return fmt.Errorf("batch item %d: %w", i, err)
		}
	}
	return nil
}

func BatchVerify(ad Adapter, items ...BatchItem) error {
	return VerifyBatch(ad, items)
}

// VerifyLinearBatch reconstructs every item's Fiat-Shamir challenges and
// per-round non-zero inverses before taking one random linear combination of
// the final verification error points. Batch weights are sampled independently;
// one transcript challenge is never reused as another item's challenge or as
// the batch weight.
func VerifyLinearBatch(ad Adapter, random io.Reader, items []BatchItem) error {
	if len(items) == 0 {
		return fmt.Errorf("%w: no proofs", ErrBatchEmpty)
	}
	if random == nil {
		random = rand.Reader
	}
	if ad == nil {
		return fmt.Errorf("%w: nil adapter", ErrAdapter)
	}
	q := ad.Order()
	if err := checkOrder(q); err != nil {
		return err
	}
	id, err := identityPoint(ad)
	if err != nil {
		return err
	}

	aggregate := clonePoint(id)
	upper := new(big.Int).Sub(q, big.NewInt(1))
	usedWeights := make(map[string]struct{}, len(items))
	if new(big.Int).SetUint64(uint64(len(items))).Cmp(upper) > 0 {
		return fmt.Errorf("%w: %d items but only %s non-zero weights", ErrBatchWeights, len(items), upper.String())
	}
	for i, item := range items {
		errorPoint, err := verificationErrorPoint(
			ad, item.DomainLabel, item.Context, item.Generators, item.Commitment, item.Proof,
		)
		if err != nil {
			return fmt.Errorf("batch item %d: %w", i, err)
		}
		var weight *big.Int
		for {
			weight, err = rand.Int(random, upper)
			if err != nil {
				return fmt.Errorf("batch weight %d: %w", i, err)
			}
			weight.Add(weight, big.NewInt(1))
			key := weight.String()
			if _, duplicated := usedWeights[key]; !duplicated {
				usedWeights[key] = struct{}{}
				break
			}
		}
		weightedError, err := ad.MultiplyPoint(weight, errorPoint)
		if err != nil {
			return fmt.Errorf("batch item %d weight: %w", i, err)
		}
		aggregate, err = ad.AddPoints(aggregate, weightedError)
		if err != nil {
			return fmt.Errorf("batch aggregate: %w", err)
		}
	}

	canonicalID, err := canonicalPoint(ad, id)
	if err != nil {
		return err
	}
	if !bytes.Equal(aggregate, canonicalID) {
		return fmt.Errorf("%w: random linear batch equation failed", ErrVerificationFailed)
	}
	return nil
}
