package upgradeplan

import (
	"fmt"
	"strings"
)

// NormalizedProblem is a validated and canonicalized planning input.
type NormalizedProblem struct {
	original    Problem
	packageName string
	belief      *Belief
	releases    []Release
	byID        map[string]Release
	target      Target
	maxRounds   int
}

type normalizedProblem = NormalizedProblem

// NormalizeProblem validates limits and normalizes all finite input fields.
// Solve and VerifyPlan call it automatically; hosts may call it to canonicalize
// an input before constructing their own session.
func NormalizeProblem(problem Problem) (*NormalizedProblem, error) {
	if err := problem.ValidateLimits(); err != nil {
		return nil, err
	}

	out := &normalizedProblem{
		original:    problem,
		packageName: strings.TrimSpace(problem.Package),
		byID:        make(map[string]Release, len(problem.Releases)),
		maxRounds:   problem.MaxRounds,
	}
	if out.maxRounds <= 0 {
		out.maxRounds = MaxRounds
	}

	fleets := problem.InitialFleets
	if len(fleets) == 0 {
		switch {
		case len(problem.Devices) > 0:
			fleets = [][]DeviceState{problem.Devices}
		case problem.DeviceCount > 0 && len(problem.InitialStates) > 0:
			fleets = AllMultisetFleets(problem.InitialStates, problem.DeviceCount)
		default:
			fleets = [][]DeviceState{nil}
		}
	}
	normalizedFleets := make([][]DeviceState, 0, len(fleets))
	for fleetIndex, rawFleet := range fleets {
		fleet := CanonicalFleet(rawFleet)
		for deviceIndex := range fleet {
			device := &fleet[deviceIndex]
			if device.Package == "" {
				device.Package = out.packageName
			}
			if err := validateSigners(device.Signers); err != nil {
				return nil, fmt.Errorf("upgradeplan: fleet %d device %d: %w", fleetIndex, deviceIndex, err)
			}
			if device.SDK < 0 {
				return nil, fmt.Errorf("upgradeplan: fleet %d device %d: SDK must be non-negative", fleetIndex, deviceIndex)
			}
		}
		normalizedFleets = append(normalizedFleets, []DeviceState(fleet))
	}
	out.belief = NewBelief(normalizedFleets...)

	seenReleaseIDs := make(map[string]struct{}, len(problem.Releases))
	for i, raw := range problem.Releases {
		rel := raw
		rel.ID = strings.TrimSpace(rel.ID)
		if rel.ID == "" {
			return nil, fmt.Errorf("upgradeplan: release %d has empty ID", i)
		}
		if _, dup := seenReleaseIDs[rel.ID]; dup {
			return nil, fmt.Errorf("upgradeplan: duplicate release ID %q", rel.ID)
		}
		seenReleaseIDs[rel.ID] = struct{}{}
		if rel.Package == "" {
			rel.Package = out.packageName
		}
		if rel.OldThreshold < 0 {
			return nil, fmt.Errorf("upgradeplan: release %q has negative OldThreshold", rel.ID)
		}
		if rel.SDKRange.MinSDK != 0 && rel.SDKRange.MaxSDK != 0 && rel.SDKRange.MinSDK > rel.SDKRange.MaxSDK {
			return nil, fmt.Errorf("upgradeplan: release %q has inverted SDK range", rel.ID)
		}

		oldSigners := append(SignerSet{}, rel.OldSigners...)
		for _, key := range rel.OldSignerKeys {
			oldSigners = append(oldSigners, Signer{ID: key, KeyMaterial: key})
		}
		rel.OldSigners = oldSigners.Normalize()
		if err := validateSigners(rel.OldSigners); err != nil {
			return nil, fmt.Errorf("upgradeplan: release %q old signers: %w", rel.ID, err)
		}

		sets := rel.NewSignerSets
		if len(sets) == 0 && len(rel.NewSigners) > 0 {
			sets = []SignerSet{rel.NewSigners}
		}
		if len(sets) == 0 {
			return nil, fmt.Errorf("upgradeplan: release %q needs at least one new signer set", rel.ID)
		}
		seenSets := make(map[string]struct{}, len(sets))
		rel.NewSignerSets = make([]SignerSet, 0, len(sets))
		for setIndex, set := range sets {
			normalizedSet := set.Normalize()
			if err := validateSigners(normalizedSet); err != nil {
				return nil, fmt.Errorf("upgradeplan: release %q new set %d: %w", rel.ID, setIndex, err)
			}
			key := normalizedSet.canonicalSetKey()
			if _, ok := seenSets[key]; ok {
				continue
			}
			seenSets[key] = struct{}{}
			rel.NewSignerSets = append(rel.NewSignerSets, normalizedSet)
		}

		out.releases = append(out.releases, rel)
		out.byID[rel.ID] = rel
	}

	target := problem.Target
	target.Trusted = target.Trusted.Normalize()
	for i := range target.TrustedSets {
		target.TrustedSets[i] = target.TrustedSets[i].Normalize()
	}
	if len(target.TrustedSets) == 0 && len(target.Trusted) == 0 {
		return nil, fmt.Errorf("upgradeplan: target trusted signer set is required")
	}
	out.target = target

	return out, nil
}

func validateSigners(signers SignerSet) error {
	for i, signer := range signers.Normalize() {
		if strings.TrimSpace(signer.ID) == "" && signer.publicMaterial() == "" {
			return fmt.Errorf("signer %d needs an ID or public key material", i)
		}
		if signer.PublicKey != "" && signer.KeyMaterial != "" && signer.PublicKey != signer.KeyMaterial {
			return fmt.Errorf("signer %q has conflicting PublicKey and KeyMaterial", signer.ID)
		}
	}
	return nil
}

func (ss SignerSet) canonicalSetKey() string {
	return "SET{" + strings.Join(ss.Materials(), ",") + "}"
}

func (np *normalizedProblem) goalFleet(fleet Fleet) (bool, string) {
	for i, device := range fleet {
		if reason := dataLossReason(device); reason != "" {
			return false, fmt.Sprintf("device %d: %s", i, reason)
		}
		if !np.target.matches(device.Signers) {
			return false, fmt.Sprintf("device %d signers %v are not trusted", i, device.Signers)
		}
	}
	return true, ""
}

func dataLossReason(device DeviceState) string {
	if device.DataLost {
		return "initial data was lost during an earlier upgrade"
	}
	if device.InitialData && !device.HasData {
		return "initial existing data marker disappeared"
	}
	return ""
}

func firstDataLossWorld(belief *Belief) (Fleet, int, string) {
	for _, fleet := range belief.Worlds {
		for i, device := range fleet {
			if reason := dataLossReason(device); reason != "" {
				return fleet, i, reason
			}
		}
	}
	return nil, -1, ""
}
