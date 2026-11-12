package modverify

import "fmt"

// Status is the outcome of cross-checking a downloaded module against the
// available trust sources.
type Status int

const (
	// Verified means both the local go.sum and the verification database
	// were available and each matches the computed hashes.
	Verified Status = iota
	// PendingReview means only one trust source was available; it matched,
	// but a second independent confirmation is still required.
	PendingReview
	// Mismatch means at least one available trust source disagrees with the
	// computed hashes; the archive must be rejected.
	Mismatch
)

func (s Status) String() string {
	switch s {
	case Verified:
		return "verified"
	case PendingReview:
		return "pending-review"
	case Mismatch:
		return "mismatch"
	}
	return "unknown"
}

// Result reports the verification outcome together with the computed hashes.
type Result struct {
	Status  Status
	ZipHash string
	ModHash string
	Detail  string
}

// VerifyOptions identifies the downloaded artifacts and the trust sources.
type VerifyOptions struct {
	ZipPath   string     // path to the downloaded module zip (required)
	ModPath   string     // path to the downloaded go.mod (required)
	Module    string     // module path, e.g. "example.org/m" (required)
	Version   string     // module version, e.g. "v1.0.0" (required)
	GoSumPath string     // local go.sum file; empty means the source is unavailable
	DB        *Reference // verification-database record; nil means unavailable
}

// VerifyModule hashes the downloaded zip and go.mod, then cross-checks them
// against every available trust source. Any mismatch yields Mismatch. When
// only one source is available and it matches, the result is PendingReview
// rather than Verified.
func VerifyModule(opts VerifyOptions) (Result, error) {
	zipHash, err := HashZip(opts.ZipPath, opts.Module, opts.Version)
	if err != nil {
		return Result{}, err
	}
	modHash, err := HashMod(opts.ModPath)
	if err != nil {
		return Result{}, err
	}

	res := Result{ZipHash: zipHash, ModHash: modHash}

	var goSumRef *Reference
	if opts.GoSumPath != "" {
		goSumRef, err = ParseGoSum(opts.GoSumPath, opts.Module, opts.Version)
		if err != nil {
			return Result{}, fmt.Errorf("modverify: reading go.sum: %w", err)
		}
	}

	sources := 0
	check := func(name string, ref *Reference) error {
		if ref == nil {
			return nil
		}
		sources++
		if ref.ZipHash != "" && ref.ZipHash != zipHash {
			res.Status = Mismatch
			res.Detail = fmt.Sprintf("%s zip hash %q != computed %q", name, ref.ZipHash, zipHash)
			return errMismatch
		}
		if ref.ModHash != "" && ref.ModHash != modHash {
			res.Status = Mismatch
			res.Detail = fmt.Sprintf("%s go.mod hash %q != computed %q", name, ref.ModHash, modHash)
			return errMismatch
		}
		return nil
	}

	if check("go.sum", goSumRef) != nil {
		return res, nil
	}
	if check("verify-db", opts.DB) != nil {
		return res, nil
	}

	if sources >= 2 {
		res.Status = Verified
		res.Detail = "go.sum and verify-db both match"
	} else {
		res.Status = PendingReview
		res.Detail = fmt.Sprintf("only %d trust source(s) available; manual review required", sources)
	}
	return res, nil
}

var errMismatch = fmt.Errorf("mismatch")
