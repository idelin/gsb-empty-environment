package netsec

import "fmt"

// Credential models the common authentication case: the client stamps a
// half-open validity window in its own clock readings, while the verifier
// only holds a reading of its own (server) clock at check time.
type Credential struct {
	// ClientClock stamps NotBefore/NotAfter and carries its closed offset
	// domain relative to true time.
	ClientClock *Clock

	// ServerClock produces ServerNow and carries its closed offset domain.
	ServerClock *Clock

	// NotBefore and NotAfter are the credential's half-open validity window,
	// expressed in client-clock seconds: [NotBefore, NotAfter).
	NotBefore int64
	NotAfter  int64

	// ServerNow is the verifier's observed server-clock reading at check
	// time, in whole seconds.
	ServerNow int64
}

// CheckCredential evaluates a two-clock credential. It is a thin, typed
// wrapper around Problem.Evaluate that registers both clocks, anchors the
// check event on the server reading and applies the client validity window.
func CheckCredential(cred Credential) (*Report, error) {
	problem := &Problem{
		Clocks: []*Clock{cred.ClientClock, cred.ServerClock},
		Now:    Instant{Clock: cred.ServerClock, Reading: cred.ServerNow},
		Windows: []Window{{
			NotBefore: Bound{Clock: cred.ClientClock, Reading: cred.NotBefore},
			NotAfter:  Bound{Clock: cred.ClientClock, Reading: cred.NotAfter},
		}},
	}
	report, err := problem.Evaluate()
	if err != nil {
		return nil, fmt.Errorf("netsec: credential check: %w", err)
	}
	return report, nil
}

// IntRange is a closed integer interval [Min, Max] in whole seconds.
type IntRange struct {
	Min int64
	Max int64
}

// Empty reports whether the interval contains no integer.
func (r IntRange) Empty() bool { return r.Min > r.Max }

// SkewReport summarizes the interval-level clock-skew analysis for a
// two-clock credential, without enumerating the offset domains.
type SkewReport struct {
	Verdict Verdict

	// RequiredClientOffset is the closed range of client offsets for which
	// the credential is currently valid (given the declared server domain).
	RequiredClientOffset IntRange

	// ClientDomain is the client clock's declared offset domain.
	ClientDomain IntRange

	// Calibration is the closed range of client offsets that would make the
	// credential valid while remaining inside the declared domain. It is the
	// intersection of RequiredClientOffset and ClientDomain; empty means no
	// calibration can rescue the credential.
	Calibration IntRange
}

// AnalyzeSkew derives the interval counterpart of CheckCredential.
//
// With server reading n_s = tau + o_s and client validity window
// [a, b), the credential is valid for offsets (o_c, o_s) exactly when
// a - (n_s - o_s) <= o_c < b - (n_s - o_s). The closed range
// RequiredClientOffset is the union of required o_c values over the server
// domain, but accept/reject/resync cannot be decided from that range alone:
// o_c and o_s are coupled, so the verdict and the feasible client-offset
// calibration are computed by enumerating the joint two-clock domain. The
// result therefore always agrees with CheckCredential.
func AnalyzeSkew(cred Credential) (*SkewReport, error) {
	if err := cred.ClientClock.Validate(); err != nil {
		return nil, fmt.Errorf("netsec: client clock: %w", err)
	}
	if err := cred.ServerClock.Validate(); err != nil {
		return nil, fmt.Errorf("netsec: server clock: %w", err)
	}
	if cred.NotBefore >= cred.NotAfter {
		return nil, ErrInvalidWindow
	}

	// o_c >= NotBefore - ServerNow + o_s, tightest at the smallest o_s.
	requiredMin := cred.NotBefore - cred.ServerNow + cred.ServerClock.MinOffset
	// o_c < NotAfter - ServerNow + o_s; integer domain, tightest at the
	// largest o_s, turning the strict bound into an inclusive Max.
	requiredMax := cred.NotAfter - cred.ServerNow + cred.ServerClock.MaxOffset - 1

	required := IntRange{Min: requiredMin, Max: requiredMax}
	domain := IntRange{Min: cred.ClientClock.MinOffset, Max: cred.ClientClock.MaxOffset}

	// Enumerate the joint domain to capture the o_c/o_s coupling precisely.
	trueNowAt := func(serverOffset int64) int64 {
		return cred.ServerNow - serverOffset
	}
	window := Window{
		NotBefore: Bound{Clock: cred.ClientClock, Reading: cred.NotBefore},
		NotAfter:  Bound{Clock: cred.ClientClock, Reading: cred.NotAfter},
	}
	var total, valid int64
	var feasibleMin, feasibleMax int64
	haveFeasible := false
	for serverOffset := cred.ServerClock.MinOffset; serverOffset <= cred.ServerClock.MaxOffset; serverOffset++ {
		trueNow := trueNowAt(serverOffset)
		for clientOffset := cred.ClientClock.MinOffset; clientOffset <= cred.ClientClock.MaxOffset; clientOffset++ {
			total++
			if window.contains(trueNow, clientOffset) {
				valid++
				if !haveFeasible {
					feasibleMin, feasibleMax = clientOffset, clientOffset
					haveFeasible = true
				} else if clientOffset < feasibleMin {
					feasibleMin = clientOffset
				} else if clientOffset > feasibleMax {
					feasibleMax = clientOffset
				}
			}
		}
	}

	var calibration IntRange
	if haveFeasible {
		calibration = IntRange{Min: feasibleMin, Max: feasibleMax}
	}

	report := &SkewReport{
		RequiredClientOffset: required,
		ClientDomain:         domain,
		Calibration:          calibration,
	}
	switch {
	case valid == 0:
		report.Verdict = VerdictReject
	case valid == total:
		report.Verdict = VerdictAccept
	default:
		report.Verdict = VerdictResync
	}
	return report, nil
}
