package taskpkg

import "fmt"

// Hop describes the fate of one fingerprint at one trust generation.
type Hop struct {
	Version  uint64
	Retained bool
}

// TrustPath is the currently valid trust chain attached to every cache item.
// It records every generation from the query anchor to the current one and
// whether the signing fingerprint was retained at each hop, so a consumer
// never has to compare version numbers to decide if an entry is trustworthy.
type TrustPath struct {
	Zone            string
	Signer          Fingerprint
	AnchoredVersion uint64
	CurrentVersion  uint64
	// Active means the signer is retained by the current declaration.
	Active bool
	Hops   []Hop
}

// BuildTrustPath constructs the full path of fp from the pinned anchor
// generation through the current declaration of its zone.
func BuildTrustPath(store *TrustStore, anchor TrustAnchor, fp Fingerprint) (TrustPath, error) {
	anchored, err := store.At(anchor.Zone, anchor.Version)
	if err != nil {
		return TrustPath{}, err
	}
	if !anchored.Allows(fp) {
		return TrustPath{}, fmt.Errorf("%w: zone=%q v%d fp=%s", ErrFingerprintNotRetained, anchor.Zone, anchor.Version, fp)
	}
	current, err := store.Current(anchor.Zone)
	if err != nil {
		return TrustPath{}, err
	}
	walk, err := store.RetentionWalk(anchor.Zone, fp)
	if err != nil {
		return TrustPath{}, err
	}
	hops := make([]Hop, len(walk))
	for i, r := range walk {
		hops[i] = Hop{Version: r.Version, Retained: r.Retained}
	}
	return TrustPath{
		Zone:            anchor.Zone,
		Signer:          fp,
		AnchoredVersion: anchor.Version,
		CurrentVersion:  current.Version,
		Active:          current.Allows(fp),
		Hops:            hops,
	}, nil
}
