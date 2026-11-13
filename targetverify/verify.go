package targetverify

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"
)

// ECUIdentity identifies the ECU that Director metadata must name.
type ECUIdentity struct {
	Serial   string
	Hardware string
}

func (id ECUIdentity) valid() bool {
	return id.Serial != "" && id.Hardware != ""
}

// State is the locally persisted rollback-protection state: the highest
// metadata version ever accepted from each repository.
type State struct {
	DirectorVersion int `json:"directorVersion"`
	ImageVersion    int `json:"imageVersion"`
}

// Config configures a Verifier.
type Config struct {
	ECU               ECUIdentity
	DirectorThreshold int
	ImageThreshold    int
	DirectorKeys      *Keyring
	ImageKeys         *Keyring
	// Now supplies the clock used for expiry checks; defaults to time.Now.
	Now func() time.Time
}

// Verifier cross-checks Director and Image targets metadata before an ECU
// installs an update. It is safe for concurrent use.
type Verifier struct {
	cfg   Config
	mu    sync.Mutex
	state State
}

// New builds a fail-closed Verifier.
func New(config Config) (*Verifier, error) {
	if !config.ECU.valid() {
		return nil, fmt.Errorf("%w: ECU serial and hardware identifier are required", ErrInvalidConfig)
	}
	if config.DirectorThreshold < 1 {
		return nil, fmt.Errorf("%w: director signature threshold must be >= 1", ErrInvalidConfig)
	}
	if config.ImageThreshold < 1 {
		return nil, fmt.Errorf("%w: image signature threshold must be >= 1", ErrInvalidConfig)
	}
	if config.DirectorKeys == nil || config.DirectorKeys.Len() == 0 {
		return nil, fmt.Errorf("%w: director keyring is empty", ErrInvalidConfig)
	}
	if config.ImageKeys == nil || config.ImageKeys.Len() == 0 {
		return nil, fmt.Errorf("%w: image keyring is empty", ErrInvalidConfig)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Verifier{cfg: config}, nil
}

// State returns a copy of the highest accepted metadata versions.
func (v *Verifier) State() State {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.state
}

// SetState restores rollback state previously persisted by the ECU.
func (v *Verifier) SetState(state State) error {
	if state.DirectorVersion < 0 || state.ImageVersion < 0 {
		return fmt.Errorf("%w: persisted versions must be >= 0", ErrInvalidConfig)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.state = state
	return nil
}

// VerifiedTarget is an update that passed every cross-check.
type VerifiedTarget struct {
	Name   string
	Length int64
	SHA256 HexDigest
}

// VerifyArtifact streams a downloaded firmware image and confirms its length
// and SHA-256 against the verified target metadata.
func (t *VerifiedTarget) VerifyArtifact(reader io.Reader) error {
	hash := sha256.New()
	length, err := io.Copy(hash, reader)
	if err != nil {
		return fmt.Errorf("targetverify: cannot hash artifact: %w", err)
	}
	if length != t.Length {
		return fmt.Errorf("%w: artifact %q length %d, want %d", ErrTargetMismatch, t.Name, length, t.Length)
	}
	if !bytes.Equal(hash.Sum(nil), t.SHA256) {
		return fmt.Errorf("%w: artifact %q sha256 mismatch", ErrTargetMismatch, t.Name)
	}
	return nil
}

// Result reports a successful verification.
type Result struct {
	// Target is the single update installable on this ECU.
	Target *VerifiedTarget
	// SkippedOneSided lists targets present in only one repository. They are
	// reported for auditing and are never installed.
	SkippedOneSided []string
	DirectorVersion int
	ImageVersion    int
}

// Verify parses raw Director and Image targets envelopes and cross-checks
// them. It returns an error (rejecting the update) unless every check passes.
func (v *Verifier) Verify(directorRaw, imageRaw []byte) (*Result, error) {
	director, err := ParseMetadata(directorRaw)
	if err != nil {
		return nil, fmt.Errorf("director: %w", err)
	}
	image, err := ParseMetadata(imageRaw)
	if err != nil {
		return nil, fmt.Errorf("image: %w", err)
	}
	return v.VerifyMetadata(director, image)
}

// VerifyMetadata runs the full cross-check on parsed metadata documents.
func (v *Verifier) VerifyMetadata(director, image *Metadata) (*Result, error) {
	if director == nil || image == nil {
		return nil, fmt.Errorf("%w: metadata must not be nil", ErrMalformed)
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	now := v.cfg.Now()
	if err := v.checkRole("director", director, v.cfg.DirectorKeys, v.cfg.DirectorThreshold, v.state.DirectorVersion, now); err != nil {
		return nil, err
	}
	if err := v.checkRole("image", image, v.cfg.ImageKeys, v.cfg.ImageThreshold, v.state.ImageVersion, now); err != nil {
		return nil, err
	}

	name, directorTarget, err := selectDirectorTarget(director.Signed.Targets, v.cfg.ECU)
	if err != nil {
		return nil, err
	}
	imageTarget, present := image.Signed.Targets[name]
	if !present {
		return nil, fmt.Errorf("%w: director target %q has no image-repository counterpart", ErrImageSideMissing, name)
	}
	if !sameFile(directorTarget.File, imageTarget.File) {
		return nil, fmt.Errorf("%w: target %q file name/length/sha256 differ between repositories", ErrTargetMismatch, name)
	}

	skipped := collectOneSided(director.Signed.Targets, image.Signed.Targets)

	v.state.DirectorVersion = director.Signed.Version
	v.state.ImageVersion = image.Signed.Version

	return &Result{
		Target: &VerifiedTarget{
			Name:   name,
			Length: directorTarget.File.Length,
			SHA256: append(HexDigest(nil), directorTarget.File.Hashes.SHA256...),
		},
		SkippedOneSided: skipped,
		DirectorVersion: director.Signed.Version,
		ImageVersion:    image.Signed.Version,
	}, nil
}

// checkRole enforces signature threshold, freshness and anti-rollback for
// one repository's document.
func (v *Verifier) checkRole(role string, metadata *Metadata, keyring *Keyring, threshold, lastVersion int, now time.Time) error {
	valid := keyring.CountValidSignatures(metadata.RawSigned, metadata.Signatures)
	if valid < threshold {
		return fmt.Errorf("%w: %s has %d valid signatures, need %d",
			ErrThresholdNotMet, role, valid, threshold)
	}
	if !now.Before(metadata.Signed.Expires) {
		return fmt.Errorf("%w: %s metadata expired at %s", ErrExpired, role, metadata.Signed.Expires.UTC().Format(time.RFC3339))
	}
	if metadata.Signed.Version < lastVersion {
		return fmt.Errorf("%w: %s version %d is below last seen version %d",
			ErrRollback, role, metadata.Signed.Version, lastVersion)
	}
	return nil
}

// selectDirectorTarget finds the unique Director target that names this ECU.
func selectDirectorTarget(targets map[string]TargetMeta, ecu ECUIdentity) (string, TargetMeta, error) {
	type match struct {
		name   string
		target TargetMeta
	}
	var matches []match
	for name, target := range targets {
		custom := target.Custom
		if custom.ECUIdentifier == ecu.Serial && custom.HardwareIdentifier == ecu.Hardware {
			matches = append(matches, match{name: name, target: target})
		}
	}
	switch {
	case len(matches) == 0:
		return "", TargetMeta{}, fmt.Errorf("%w: no target names ecu %q / hardware %q",
			ErrNoTargetForECU, ecu.Serial, ecu.Hardware)
	case len(matches) > 1:
		return "", TargetMeta{}, fmt.Errorf("%w: %d director targets name ecu %q, expected exactly one",
			ErrECUMismatch, len(matches), ecu.Serial)
	}
	return matches[0].name, matches[0].target, nil
}

// collectOneSided returns the sorted names of targets listed by only one side.
func collectOneSided(director, image map[string]TargetMeta) []string {
	seen := make(map[string]struct{}, len(director)+len(image))
	for name := range director {
		if _, ok := image[name]; !ok {
			seen[name] = struct{}{}
		}
	}
	for name := range image {
		if _, ok := director[name]; !ok {
			seen[name] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sameFile compares the immutable file attributes shared by both repositories.
func sameFile(a, b TargetFile) bool {
	return a.Length == b.Length && bytes.Equal(a.Hashes.SHA256, b.Hashes.SHA256)
}
