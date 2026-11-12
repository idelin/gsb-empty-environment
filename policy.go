// Package netsec provides policy-digest evaluation for TPM-backed device
// provisioning.
//
// A policy is an ordered AND of steps. Each step either extends the current
// policy digest with a caller-selected command code and parameters, or
// performs a PolicyOR operation. Evaluation starts from an all-zero digest,
// exactly like a newly created TPM policy session.
//
// PolicyOR must be configured with the exact branch digests belonging to the
// object's policy. Those digests are trusted policy data; they must never be
// copied from a provisioning request or from an untrusted session trace.
// Evaluation commits to every digest in that list, and a policy is accepted
// only when its final digest matches the TPM object's authPolicy.
package netsec

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// DigestSize is the size, in bytes, of a SHA-256 TPM policy digest.
	DigestSize = sha256.Size

	// PolicyDigestSize is an alias for DigestSize.
	PolicyDigestSize = DigestSize

	// MaxORBranches is the largest branch list accepted by PolicyOR.
	MaxORBranches = 8
)

// CommandCode is a TPM command code (TPM_CC).
type CommandCode uint32

// CommandCodeTable maps policy command names to TPM command codes. The table
// is supplied by the caller so that the service controls which command code is
// used for each step. A step with an empty command name is invalid, except for
// a PolicyOR step, which defaults to KeyPolicyOR.
type CommandCodeTable map[string]CommandCode

// CommandCodes is an alias for CommandCodeTable.
type CommandCodes = CommandCodeTable

// Conventional command-table keys. Callers may use other keys as long as the
// corresponding steps use the same names.
const (
	KeyPolicyPCR         = "PolicyPCR"
	KeyPolicyOR          = "PolicyOR"
	KeyPolicyCommandCode = "PolicyCommandCode"
	KeyPolicyAuthValue   = "PolicyAuthValue"
	KeyPolicyNV          = "PolicyNV"
)

// TPM 2.0 command codes for the commonly used policy commands.
const (
	TPMCCPolicyPCR         CommandCode = 0x0000017F
	TPMCCPolicyOR          CommandCode = 0x00000171
	TPMCCPolicyCommandCode CommandCode = 0x0000016C
	TPMCCPolicyAuthValue   CommandCode = 0x0000016B
	TPMCCPolicyNV          CommandCode = 0x00000149
)

// DefaultCommandCodeTable returns the conventional TPM command-code mapping.
// Callers that need a restricted or non-standard mapping should build their
// own table instead.
func DefaultCommandCodeTable() CommandCodeTable {
	return CommandCodeTable{
		KeyPolicyPCR:         TPMCCPolicyPCR,
		KeyPolicyOR:          TPMCCPolicyOR,
		KeyPolicyCommandCode: TPMCCPolicyCommandCode,
		KeyPolicyAuthValue:   TPMCCPolicyAuthValue,
		KeyPolicyNV:          TPMCCPolicyNV,
	}
}

var (
	// ErrInvalidDigest indicates a digest with the wrong length or a malformed
	// PolicyOR branch list.
	ErrInvalidDigest = errors.New("netsec: invalid policy digest")

	// ErrInvalidAuthPolicy indicates that the object authPolicy is malformed.
	ErrInvalidAuthPolicy = errors.New("netsec: invalid object authPolicy")

	// ErrUnknownCommand indicates that a policy step references a command name
	// that is absent from the caller-supplied command-code table.
	ErrUnknownCommand = errors.New("netsec: unknown policy command")

	// ErrUnknownStepKind indicates a zero or unsupported step kind.
	ErrUnknownStepKind = errors.New("netsec: unknown policy step kind")

	// ErrEmptyORBranchList indicates a PolicyOR without any branches.
	ErrEmptyORBranchList = errors.New("netsec: PolicyOR branch list is empty")

	// ErrTooManyORBranches indicates a PolicyOR with more than eight branches.
	ErrTooManyORBranches = errors.New("netsec: PolicyOR has more than 8 branches")

	// ErrORBranchNotMatched indicates that the digest immediately before the
	// PolicyOR step is not one of the trusted branch digests. This rejects
	// sessions that try to graft an unrelated object's policy onto an OR.
	ErrORBranchNotMatched = errors.New("netsec: current digest is not a PolicyOR branch")

	// ErrPolicyMismatch indicates that evaluation completed but did not
	// reproduce the object's authPolicy.
	ErrPolicyMismatch = errors.New("netsec: policy digest does not match object authPolicy")

	// ErrNilEvaluator indicates use of a nil Evaluator.
	ErrNilEvaluator = errors.New("netsec: nil evaluator")
)

// ZeroDigest returns a fresh, all-zero policy digest.
func ZeroDigest() []byte {
	return make([]byte, DigestSize)
}

// ExtendDigest computes H(old || commandCode || params...). The command code is
// encoded as a big-endian 32-bit value. Parameter slices are concatenated in
// order with no separators, matching TPM command hashing.
func ExtendDigest(old []byte, commandCode CommandCode, params ...[]byte) ([]byte, error) {
	if len(old) != DigestSize {
		return nil, fmt.Errorf("%w: old digest has length %d, want %d", ErrInvalidDigest, len(old), DigestSize)
	}

	h := sha256.New()
	_, _ = h.Write(old)
	var encodedCode [4]byte
	binary.BigEndian.PutUint32(encodedCode[:], uint32(commandCode))
	_, _ = h.Write(encodedCode[:])
	for _, param := range params {
		_, _ = h.Write(param)
	}
	return h.Sum(nil), nil
}

// PolicyORDigest computes H(0 || commandCode || branches...). The leading zero
// digest is the reset required by TPM PolicyOR. branches must contain between
// one and eight SHA-256 digests, in the exact order committed by the policy.
func PolicyORDigest(commandCode CommandCode, branches [][]byte) ([]byte, error) {
	if err := validateORBranches(branches); err != nil {
		return nil, err
	}

	h := sha256.New()
	_, _ = h.Write(make([]byte, DigestSize))
	var encodedCode [4]byte
	binary.BigEndian.PutUint32(encodedCode[:], uint32(commandCode))
	_, _ = h.Write(encodedCode[:])
	for _, branch := range branches {
		_, _ = h.Write(branch)
	}
	return h.Sum(nil), nil
}

// MatchesORBranch reports whether current is one of the branch digests. The
// comparison scans the whole list and uses constant-time digest comparison.
func MatchesORBranch(current []byte, branches [][]byte) bool {
	if len(current) != DigestSize {
		return false
	}

	matched := 0
	for _, branch := range branches {
		if len(branch) != DigestSize {
			continue
		}
		matched |= subtle.ConstantTimeCompare(current, branch)
	}
	return matched == 1
}

func validateORBranches(branches [][]byte) error {
	if len(branches) == 0 {
		return ErrEmptyORBranchList
	}
	if len(branches) > MaxORBranches {
		return fmt.Errorf("%w: got %d", ErrTooManyORBranches, len(branches))
	}
	for i, branch := range branches {
		if len(branch) != DigestSize {
			return fmt.Errorf("%w: branch %d has length %d, want %d", ErrInvalidDigest, i, len(branch), DigestSize)
		}
	}
	return nil
}

// StepKind selects how a policy step transforms the running digest.
type StepKind uint8

const (
	// StepExtend appends a command code and parameters to the running digest.
	StepExtend StepKind = iota + 1

	// StepPolicyOR verifies that the running digest is one of the trusted
	// branches, then replaces it with the PolicyOR digest.
	StepPolicyOR
)

// Step is one operation in an ordered AND policy.
//
// For StepExtend, Command names an entry in CommandCodeTable and Params are
// hashed in order after the command code. For StepPolicyOR, Branches must be
// the complete, trusted branch-digest list; Command names the PolicyOR command
// in the table and may be left empty to use KeyPolicyOR.
type Step struct {
	Kind     StepKind
	Command  string
	Params   [][]byte
	Branches [][]byte
}

// Extend builds an AND step that hashes command || params into the running
// policy digest.
func Extend(command string, params ...[]byte) Step {
	return Step{Kind: StepExtend, Command: command, Params: params}
}

// Command is an alias for Extend.
func Command(command string, params ...[]byte) Step {
	return Extend(command, params...)
}

// NewExtendStep is an alias for Extend.
func NewExtendStep(command string, params ...[]byte) Step {
	return Extend(command, params...)
}

// OR builds a PolicyOR step using the conventional KeyPolicyOR table entry.
// branches is the trusted branch-digest list, not a list supplied by a caller
// at verification time.
func OR(branches ...[]byte) Step {
	return ORWithCommand(KeyPolicyOR, branches...)
}

// PolicyOR is an alias for OR.
func PolicyOR(branches ...[]byte) Step {
	return OR(branches...)
}

// ORWithCommand builds a PolicyOR step using a caller-selected command-table
// key. This is useful when the device service uses a non-standard mapping.
func ORWithCommand(command string, branches ...[]byte) Step {
	return Step{Kind: StepPolicyOR, Command: command, Branches: branches}
}

// NewORStep builds a PolicyOR step using the conventional KeyPolicyOR key.
func NewORStep(branches ...[]byte) Step {
	return OR(branches...)
}

// NewORStepWithCommand is an alias for ORWithCommand.
func NewORStepWithCommand(command string, branches ...[]byte) Step {
	return ORWithCommand(command, branches...)
}

// Policy is an ordered AND of steps. Every step is evaluated in order.
type Policy struct {
	Steps []Step
}

// NewPolicy builds a policy from an ordered list of AND steps.
func NewPolicy(steps ...Step) Policy {
	return Policy{Steps: steps}
}

// EvaluateDigest evaluates the policy and returns its final digest. It does
// not perform an authorization decision.
func EvaluateDigest(policy Policy, commands CommandCodeTable) ([]byte, error) {
	digest := ZeroDigest()

	for i, step := range policy.Steps {
		switch step.Kind {
		case StepExtend:
			if step.Command == "" {
				return nil, fmt.Errorf("step %d: %w: empty command name", i, ErrUnknownCommand)
			}
			code, err := lookupCommand(commands, step.Command)
			if err != nil {
				return nil, fmt.Errorf("step %d: %w", i, err)
			}
			digest, err = ExtendDigest(digest, code, step.Params...)
			if err != nil {
				return nil, fmt.Errorf("step %d: %w", i, err)
			}

		case StepPolicyOR:
			if err := validateORBranches(step.Branches); err != nil {
				return nil, fmt.Errorf("step %d: %w", i, err)
			}
			command := step.Command
			if command == "" {
				command = KeyPolicyOR
			}
			code, err := lookupCommand(commands, command)
			if err != nil {
				return nil, fmt.Errorf("step %d: %w", i, err)
			}
			if !MatchesORBranch(digest, step.Branches) {
				return nil, fmt.Errorf("step %d: %w", i, ErrORBranchNotMatched)
			}
			digest, err = PolicyORDigest(code, step.Branches)
			if err != nil {
				return nil, fmt.Errorf("step %d: %w", i, err)
			}

		default:
			return nil, fmt.Errorf("step %d: %w: %d", i, ErrUnknownStepKind, step.Kind)
		}
	}

	return digest, nil
}

// Evaluate is the provisioning gate: it evaluates an ordered AND policy and
// releases only when the final digest exactly matches authPolicy. The branch
// lists inside PolicyOR steps must come from trusted object metadata.
func Evaluate(policy Policy, commands CommandCodeTable, authPolicy []byte) error {
	if len(authPolicy) != DigestSize {
		return fmt.Errorf("%w: length %d, want %d", ErrInvalidAuthPolicy, len(authPolicy), DigestSize)
	}

	digest, err := EvaluateDigest(policy, commands)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(digest, authPolicy) != 1 {
		return ErrPolicyMismatch
	}
	return nil
}

// Authorize is an alias for Evaluate, named for the provisioning call site.
func Authorize(policy Policy, commands CommandCodeTable, authPolicy []byte) error {
	return Evaluate(policy, commands, authPolicy)
}

// AuthorizePolicy is an alias for Evaluate.
func AuthorizePolicy(policy Policy, commands CommandCodeTable, authPolicy []byte) error {
	return Evaluate(policy, commands, authPolicy)
}

// Evaluate evaluates the policy against the object authPolicy.
func (p Policy) Evaluate(commands CommandCodeTable, authPolicy []byte) error {
	return Evaluate(p, commands, authPolicy)
}

// Authorize evaluates the policy against the object authPolicy.
func (p Policy) Authorize(commands CommandCodeTable, authPolicy []byte) error {
	return Evaluate(p, commands, authPolicy)
}

// Digest evaluates the policy and returns its final digest.
func (p Policy) Digest(commands CommandCodeTable) ([]byte, error) {
	return EvaluateDigest(p, commands)
}

// Evaluator binds a caller-supplied command-code table for repeated use by a
// provisioning service.
type Evaluator struct {
	Commands CommandCodeTable
}

// NewEvaluator returns an evaluator that resolves policy command names through
// commands.
func NewEvaluator(commands CommandCodeTable) *Evaluator {
	return &Evaluator{Commands: commands}
}

// Digest evaluates the policy and returns its final digest.
func (e *Evaluator) Digest(policy Policy) ([]byte, error) {
	if e == nil {
		return nil, ErrNilEvaluator
	}
	return EvaluateDigest(policy, e.Commands)
}

// Evaluate authorizes the policy only when its final digest matches authPolicy.
func (e *Evaluator) Evaluate(policy Policy, authPolicy []byte) error {
	if e == nil {
		return ErrNilEvaluator
	}
	return Evaluate(policy, e.Commands, authPolicy)
}

// Authorize is an alias for Evaluate.
func (e *Evaluator) Authorize(policy Policy, authPolicy []byte) error {
	return e.Evaluate(policy, authPolicy)
}

func lookupCommand(commands CommandCodeTable, name string) (CommandCode, error) {
	if commands == nil {
		return 0, fmt.Errorf("%w: %q (command table is nil)", ErrUnknownCommand, name)
	}
	code, ok := commands[name]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownCommand, name)
	}
	return code, nil
}
