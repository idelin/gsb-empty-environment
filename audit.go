package netsec

import (
	"fmt"
	"strings"
)

// ClaimTuple is a canonical claim assignment. Returned collision tuples are
// independent copies and can be inspected or retained by the caller.
type ClaimTuple map[ClaimName]string

// Collision describes two distinct claim tuples that produce one principal.
// First and Second are ordered lexicographically in canonical claim order:
// sub, namespace, serviceAccount, repository, ref.
type Collision struct {
	Principal string
	First     ClaimTuple
	Second    ClaimTuple
}

// CollisionError is returned by Publish when auditing finds a collision.
type CollisionError struct {
	Collision Collision
}

func (e *CollisionError) Error() string {
	if e == nil {
		return ErrCollision.Error()
	}
	return fmt.Sprintf(
		"%s: principal %q from tuples %s and %s",
		ErrCollision,
		e.Collision.Principal,
		formatTuple(e.Collision.First),
		formatTuple(e.Collision.Second),
	)
}

func (e *CollisionError) Is(target error) bool {
	return target == ErrCollision
}

func (e *CollisionError) Unwrap() error {
	return ErrCollision
}

func formatTuple(tuple ClaimTuple) string {
	var builder strings.Builder
	builder.WriteByte('{')
	first := true
	for _, claim := range canonicalClaimOrder {
		value, ok := tuple[claim]
		if !ok {
			continue
		}
		if !first {
			builder.WriteString(", ")
		}
		first = false
		builder.WriteString(string(claim))
		builder.WriteString("=")
		builder.WriteString(fmt.Sprintf("%q", value))
	}
	builder.WriteByte('}')
	return builder.String()
}

// Audit validates a configuration and exhaustively searches the finite claim
// language product for duplicates. If the complete product fits within
// AuditBudget and contains no collision, Audit returns (nil, nil). If a
// collision exists it returns the lexicographically smallest collision.
// Exhausting the budget returns ErrAuditBudget; uniqueness is never assumed.
func Audit(config ProviderConfig) (*Collision, error) {
	snapshot, err := prepareConfig(config)
	if err != nil {
		return nil, err
	}
	return auditSnapshot(snapshot)
}

// FindCollision is an alias for Audit.
func FindCollision(config ProviderConfig) (*Collision, error) {
	return Audit(config)
}

func auditSnapshot(snapshot *configSnapshot) (*Collision, error) {
	transducer := newTransducer(snapshot)
	seen := make(map[string]int)
	tuple := make(ClaimTuple, len(snapshot.claimsInAudit))
	examined := 0

	var found *Collision
	var auditErr error

	var walk func(depth int)
	walk = func(depth int) {
		if found != nil || auditErr != nil {
			return
		}
		if depth == len(snapshot.claimsInAudit) {
			if examined >= snapshot.auditBudget {
				auditErr = fmt.Errorf(
					"%w: stopped after %d claim tuples",
					ErrAuditBudget,
					examined,
				)
				return
			}
			examined++

			principal, err := transducer.transform(tuple)
			if err != nil {
				auditErr = err
				return
			}
			if previousIndex, exists := seen[principal]; exists {
				first := tupleAtIndex(snapshot, previousIndex)
				second := copyTuple(tuple)
				if compareTuples(first, second) >= 0 {
					first, second = second, first
				}
				found = &Collision{
					Principal: principal,
					First:     copyTuple(first),
					Second:    copyTuple(second),
				}
				return
			}
			seen[principal] = examined - 1
			return
		}

		claim := snapshot.claimsInAudit[depth]
		values := snapshot.languages[claim]
		for index := 0; index < values.len(); index++ {
			tuple[claim] = values.wordAt(index)
			walk(depth + 1)
			if found != nil || auditErr != nil {
				return
			}
		}
		delete(tuple, claim)
	}

	walk(0)
	if auditErr != nil {
		return nil, auditErr
	}
	return found, nil
}

func tupleAtIndex(snapshot *configSnapshot, index int) ClaimTuple {
	tuple := make(ClaimTuple, len(snapshot.claimsInAudit))
	for position := len(snapshot.claimsInAudit) - 1; position >= 0; position-- {
		claim := snapshot.claimsInAudit[position]
		language := snapshot.languages[claim]
		tuple[claim] = language.wordAt(index % language.len())
		index /= language.len()
	}
	return tuple
}

func copyTuple(tuple ClaimTuple) ClaimTuple {
	copied := make(ClaimTuple, len(tuple))
	for claim, value := range tuple {
		copied[claim] = value
	}
	return copied
}
