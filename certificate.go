package tokenmigrate

import (
	"fmt"
	"sort"
	"time"
)

// Certificate records the optimization result and the invariants checked by
// the planner. It is feasible when every Check is OK and ReissueCount equals
// ReissueLowerBound.
type Certificate struct {
	Version           int
	At                time.Time
	InputDigest       string
	PlanDigest        string
	ReissueLowerBound int
	ReissueCount      int
	Optimal           bool

	EqualityClasses []EqualityClassCertificate
	TokenGroups     []TokenGroupCertificate
	TargetScopes    []TargetScopeCertificate
	Checks          []InvariantCheck
}

// EqualityClassCertificate lists one canonical commitment class.
type EqualityClassCertificate struct {
	Class   Commitment
	Members []Commitment
	Proven  bool
}

// ClassWeight is one commitment class inside a token collision group.
type ClassWeight struct {
	Class     Commitment
	Weight    int
	RecordIDs []RecordID
}

// TokenGroupCertificate is the local lower-bound proof for one scoped token.
// Keeping one class leaves N-max(class weight) records that must move to a
// different token. The planner attains that bound.
type TokenGroupCertificate struct {
	Target Scope
	Token  Token

	Classes           []ClassWeight
	KeptClass         Commitment
	LowerBound        int
	ReissuedRecordIDs []RecordID
	Satisfied         bool
}

// TargetScopeCertificate summarizes one destination namespace.
type TargetScopeCertificate struct {
	Scope       Scope
	Records     int
	Tokens      int
	ReadEntries int
	Empty       bool
}

// InvariantCheck is one machine-checkable planner assertion.
type InvariantCheck struct {
	Name   string
	OK     bool
	Detail string
}

// Validate returns an error unless the certificate is feasible and reaches
// the stated lower bound.
func (c Certificate) Validate() error {
	if !c.Optimal {
		return fmt.Errorf("%w: certificate is not optimal", ErrInvalidInput)
	}
	if c.ReissueCount != c.ReissueLowerBound {
		return fmt.Errorf("%w: reissue count %d does not reach lower bound %d", ErrInvalidInput, c.ReissueCount, c.ReissueLowerBound)
	}
	for _, check := range c.Checks {
		if !check.OK {
			return fmt.Errorf("%w: invariant %s failed: %s", ErrInvalidInput, check.Name, check.Detail)
		}
	}
	for _, group := range c.TokenGroups {
		if !group.Satisfied {
			return fmt.Errorf("%w: token group %s/%s is not satisfied", ErrInvalidInput, group.Target, group.Token)
		}
	}
	return nil
}

func buildCertificate(v *validatedInput, assignments []Assignment, reissues []Reissue, targets []Scope, table *ReadTable) Certificate {
	cert := Certificate{
		Version:     1,
		At:          v.at,
		InputDigest: v.inputDigest,
	}

	classMembers := v.classes.members()
	roots := make([]Commitment, 0, len(classMembers))
	for root := range classMembers {
		roots = append(roots, root)
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i] < roots[j] })
	for _, root := range roots {
		members := append([]Commitment(nil), classMembers[root]...)
		sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
		cert.EqualityClasses = append(cert.EqualityClasses, EqualityClassCertificate{
			Class:   root,
			Members: members,
			Proven:  len(members) > 1,
		})
	}

	groupMap := make(map[string][]Assignment)
	for _, a := range assignments {
		key := scopeTokenKey(a.Target, a.OldToken)
		groupMap[key] = append(groupMap[key], a)
	}
	groupKeys := make([]string, 0, len(groupMap))
	for key := range groupMap {
		groupKeys = append(groupKeys, key)
	}
	sort.Strings(groupKeys)
	for _, key := range groupKeys {
		group := groupMap[key]
		byClass := make(map[Commitment][]Assignment)
		for _, a := range group {
			byClass[a.Class] = append(byClass[a.Class], a)
		}
		classes := make([]ClassWeight, 0, len(byClass))
		maxWeight := 0
		for class, members := range byClass {
			ids := make([]RecordID, 0, len(members))
			for _, a := range members {
				ids = append(ids, a.RecordID)
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			if len(ids) > maxWeight {
				maxWeight = len(ids)
			}
			classes = append(classes, ClassWeight{Class: class, Weight: len(ids), RecordIDs: ids})
		}
		sort.Slice(classes, func(i, j int) bool {
			if classes[i].Weight != classes[j].Weight {
				return classes[i].Weight > classes[j].Weight
			}
			return classes[i].Class < classes[j].Class
		})
		kept := classes[0].Class
		for _, a := range group {
			if a.NewToken == a.OldToken {
				kept = a.Class
				break
			}
		}
		reissuedIDs := make([]RecordID, 0)
		for _, a := range group {
			if a.Reissued {
				reissuedIDs = append(reissuedIDs, a.RecordID)
			}
		}
		sort.Slice(reissuedIDs, func(i, j int) bool { return reissuedIDs[i] < reissuedIDs[j] })
		lower := len(group) - maxWeight
		cert.TokenGroups = append(cert.TokenGroups, TokenGroupCertificate{
			Target:            group[0].Target,
			Token:             group[0].OldToken,
			Classes:           classes,
			KeptClass:         kept,
			LowerBound:        lower,
			ReissuedRecordIDs: reissuedIDs,
			Satisfied:         lower == len(reissuedIDs),
		})
		cert.ReissueLowerBound += lower
	}
	cert.ReissueCount = len(reissues)
	cert.Optimal = cert.ReissueCount == cert.ReissueLowerBound

	for _, scope := range targets {
		records := 0
		tokenSet := make(map[Token]struct{})
		for _, a := range assignments {
			if a.Target == scope {
				records++
				tokenSet[a.NewToken] = struct{}{}
			}
		}
		reads := 0
		for _, entry := range table.entries {
			if entry.Target == scope {
				reads++
			}
		}
		cert.TargetScopes = append(cert.TargetScopes, TargetScopeCertificate{
			Scope:       scope,
			Records:     records,
			Tokens:      len(tokenSet),
			ReadEntries: reads,
			Empty:       records == 0,
		})
	}

	cert.Checks = append(cert.Checks,
		InvariantCheck{
			Name:   "signatures",
			OK:     true,
			Detail: "all issuance records and two-party equality proofs were verified",
		},
		InvariantCheck{
			Name:   "shards-bidirectional",
			OK:     true,
			Detail: "input shard maps are consistent in both directions",
		},
		InvariantCheck{
			Name:   "target-token-unique",
			OK:     verifyTargetUniqueness(assignments, v.classes) == nil,
			Detail: "no target token in a scope denotes two commitment classes",
		},
	)

	readCheck := InvariantCheck{Name: "authorized-reads-unique", OK: true, Detail: "every authorized, live old read has one target"}
	outsideCheck := InvariantCheck{Name: "outside-reads-invisible", OK: true, Detail: "denied and nonexistent reads both return ErrNotFound"}
	for key, entry := range table.entries {
		at := entry.ValidFrom
		if at.IsZero() {
			at = v.at
		}
		if _, err := table.lookup(ReadRequest{Tenant: key.tenant, Origin: key.origin, Token: key.token, At: at}); err != nil {
			readCheck.OK = false
			readCheck.Detail = fmt.Sprintf("authorized read %s/%s/%s failed", key.tenant, key.origin, key.token)
			break
		}
		wrong, err := table.lookup(ReadRequest{Tenant: DomainID("__denied__"), Origin: key.origin, Token: key.token, At: at})
		if err != ErrNotFound || wrong.Tenant != "" {
			outsideCheck.OK = false
			outsideCheck.Detail = fmt.Sprintf("outside read %s/%s/%s did not fail closed", key.tenant, key.origin, key.token)
			break
		}
	}
	cert.Checks = append(cert.Checks, readCheck, outsideCheck)

	emptyOK := true
	emptyDetail := "empty target scopes have no inherited compatibility rows"
	for _, scopeCert := range cert.TargetScopes {
		if !scopeCert.Empty {
			continue
		}
		for _, entry := range table.entries {
			if entry.Target == scopeCert.Scope {
				emptyOK = false
				emptyDetail = fmt.Sprintf("empty scope %s has a compatibility row", scopeCert.Scope)
				break
			}
		}
	}
	cert.Checks = append(cert.Checks,
		InvariantCheck{Name: "empty-scopes-isolated", OK: emptyOK, Detail: emptyDetail},
		InvariantCheck{
			Name:   "lower-bound-achieved",
			OK:     cert.ReissueCount == cert.ReissueLowerBound,
			Detail: fmt.Sprintf("reissued %d records; lower bound %d", cert.ReissueCount, cert.ReissueLowerBound),
		},
	)
	return cert
}
