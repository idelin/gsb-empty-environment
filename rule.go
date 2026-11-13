package netsec

import (
	"encoding/hex"
	"fmt"
	"reflect"
)

// Verdict is the result of a detection rule. Unknown is the default and is
// deliberately distinct from Benign and Malicious. Unpacking or writing code
// never by itself produces Malicious.
type Verdict uint8

const (
	VerdictUnknown Verdict = iota
	VerdictBenign
	VerdictMalicious
)

func (v Verdict) String() string {
	switch v {
	case VerdictUnknown:
		return "unknown"
	case VerdictBenign:
		return "benign"
	case VerdictMalicious:
		return "malicious"
	default:
		return fmt.Sprintf("verdict(%d)", uint8(v))
	}
}

// Detection describes whether a rule ran for an execution and whether the
// result came from the detection cache.
type Detection struct {
	Verdict   Verdict
	Label     string
	Evaluated bool
	Cached    bool
}

// DetectionKey is the detection cache key. The physical generation is part of
// the key so that writing one byte and restoring it cannot reuse a stale rule
// result even though the final digest is unchanged.
type DetectionKey struct {
	PhysicalID         PhysicalID
	PhysicalGeneration uint64
	Digest             [32]byte
}

type detectionKey = DetectionKey

type cachedDetection struct {
	verdict Verdict
	label   string
}

// CacheStats reports detection rule cache activity.
type CacheStats struct {
	Entries int
	Hits    uint64
	Misses  uint64
}

// Rule is the strongly typed detection callback interface. Implementations
// must treat snapshot as immutable: it is a private copy that the tracker does
// not use again.
type Rule interface {
	Evaluate(digest [32]byte, snapshot []byte) Verdict
}

// RuleFunc adapts a function to Rule.
type RuleFunc func(digest [32]byte, snapshot []byte) Verdict

// Evaluate implements Rule.
func (f RuleFunc) Evaluate(digest [32]byte, snapshot []byte) Verdict {
	return f(digest, snapshot)
}

// SetRule installs a detection callback. The callback receives only the
// immutable full-page digest and a private copy of the 64-byte snapshot. To
// keep the interface easy to use from a host, common function shapes are
// accepted, including:
//
//	func([32]byte, []byte) bool
//	func([32]byte, []byte) Verdict
//	func([32]byte, []byte) (Verdict, error)
//	func([]byte) bool
//
// A bool result means true = malicious, false = benign. Passing nil clears the
// rule.
func (t *Tracker) SetRule(rule any) error {
	if err := validateRule(rule); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initLocked()
	t.rule = rule
	t.ruleGen++
	if t.ruleGen == 0 {
		t.ruleGen++
	}
	t.detections = make(map[detectionKey]cachedDetection)
	t.cacheStats = CacheStats{}
	return nil
}

// SetDetectionRule is an alias for SetRule.
func (t *Tracker) SetDetectionRule(rule any) error { return t.SetRule(rule) }

// SetRuleCallback is an alias for SetRule.
func (t *Tracker) SetRuleCallback(rule any) error { return t.SetRule(rule) }

// ClearRule removes the detection callback and its cache.
func (t *Tracker) ClearRule() { _ = t.SetRule(nil) }

// CacheStats returns the current detection cache statistics.
func (t *Tracker) CacheStats() CacheStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initLocked()
	stats := t.cacheStats
	stats.Entries = len(t.detections)
	return stats
}

// ResetDetectionCache clears cached rule results and statistics.
func (t *Tracker) ResetDetectionCache() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initLocked()
	t.detections = make(map[detectionKey]cachedDetection)
	t.cacheStats = CacheStats{}
}

var (
	errorType  = reflect.TypeOf((*error)(nil)).Elem()
	bytesType  = reflect.TypeOf([]byte(nil))
	digestType = reflect.TypeOf([32]byte{})
	anyType    = reflect.TypeOf((*any)(nil)).Elem()
	verdictRT  = reflect.TypeOf(Verdict(0))
	intType    = reflect.TypeOf(int(0))
	boolType   = reflect.TypeOf(false)
	stringType = reflect.TypeOf("")
)

func validateRule(rule any) error {
	if rule == nil {
		return nil
	}
	if _, ok := rule.(Rule); ok {
		return nil
	}
	rv := reflect.ValueOf(rule)
	if rv.Kind() != reflect.Func {
		return fmt.Errorf("%w: %T is not a function or Rule", ErrInvalidRule, rule)
	}
	rt := rv.Type()
	if rt.IsVariadic() {
		return fmt.Errorf("%w: variadic callbacks are not supported", ErrInvalidRule)
	}
	if rt.NumIn() < 1 || rt.NumIn() > 2 {
		return fmt.Errorf("%w: callback needs 1 or 2 inputs", ErrInvalidRule)
	}
	for i := 0; i < rt.NumIn(); i++ {
		if !supportedRuleInput(rt.In(i)) {
			return fmt.Errorf("%w: unsupported input %s", ErrInvalidRule, rt.In(i))
		}
	}
	if rt.NumOut() > 2 {
		return fmt.Errorf("%w: callback has too many outputs", ErrInvalidRule)
	}
	for i := 0; i < rt.NumOut(); i++ {
		if !supportedRuleOutput(rt.Out(i)) {
			return fmt.Errorf("%w: unsupported output %s", ErrInvalidRule, rt.Out(i))
		}
	}
	return nil
}

func supportedRuleInput(t reflect.Type) bool {
	return t == bytesType || t == digestType || t == stringType || t == anyType || t.Kind() == reflect.Interface
}

func supportedRuleOutput(t reflect.Type) bool {
	if t == boolType || t == verdictRT || t == intType || t == stringType {
		return true
	}
	return t.Implements(errorType)
}

func callRule(rule any, digest [32]byte, snapshot []byte) (verdict Verdict, label string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			verdict = VerdictUnknown
			label = ""
			err = fmt.Errorf("%w: panic: %v", ErrRuleFailed, recovered)
		}
	}()

	if rule == nil {
		return VerdictUnknown, "", nil
	}
	if r, ok := rule.(Rule); ok {
		return r.Evaluate(digest, snapshot), "", nil
	}

	rv := reflect.ValueOf(rule)
	args, err := buildRuleArguments(rv.Type(), digest, snapshot)
	if err != nil {
		return VerdictUnknown, "", err
	}
	outs := rv.Call(args)
	return parseRuleOutputs(outs)
}

func buildRuleArguments(rt reflect.Type, digest [32]byte, snapshot []byte) ([]reflect.Value, error) {
	switch rt.NumIn() {
	case 1:
		arg, err := ruleArgument(rt.In(0), digest, snapshot, ruleInputDigest(rt.In(0)))
		if err != nil {
			return nil, err
		}
		return []reflect.Value{arg}, nil
	case 2:
		firstDigest := isDigestInput(rt.In(0))
		secondDigest := isDigestInput(rt.In(1))
		if isBytesInput(rt.In(0)) && isBytesInput(rt.In(1)) {
			firstDigest = true
			secondDigest = false
		} else if isBytesInput(rt.In(0)) && secondDigest {
			firstDigest = false
			secondDigest = true
		}
		a0, err := ruleArgument(rt.In(0), digest, snapshot, firstDigest)
		if err != nil {
			return nil, err
		}
		a1, err := ruleArgument(rt.In(1), digest, snapshot, secondDigest)
		if err != nil {
			return nil, err
		}
		return []reflect.Value{a0, a1}, nil
	default:
		return nil, fmt.Errorf("%w: callback needs 1 or 2 inputs", ErrInvalidRule)
	}
}

func ruleInputDigest(t reflect.Type) bool {
	// A single []byte argument is interpreted as the snapshot. A single
	// [32]byte or string argument is interpreted as the digest.
	return t == digestType || t == stringType
}

func isDigestInput(t reflect.Type) bool {
	return t == digestType || t == stringType
}

func isBytesInput(t reflect.Type) bool {
	return t == bytesType || t == anyType || t.Kind() == reflect.Interface
}

func ruleArgument(t reflect.Type, digest [32]byte, snapshot []byte, wantDigest bool) (reflect.Value, error) {
	switch {
	case t == digestType:
		if !wantDigest {
			return reflect.Value{}, fmt.Errorf("%w: [32]byte argument must be the digest", ErrInvalidRule)
		}
		return reflect.ValueOf(digest), nil
	case t == bytesType:
		if wantDigest {
			raw := append([]byte(nil), digest[:]...)
			return reflect.ValueOf(raw), nil
		}
		return reflect.ValueOf(snapshot), nil
	case t == stringType:
		if wantDigest {
			return reflect.ValueOf(hex.EncodeToString(digest[:])), nil
		}
		return reflect.ValueOf(string(snapshot)), nil
	case t == anyType || t.Kind() == reflect.Interface:
		var value any
		if wantDigest {
			value = digest
		} else {
			value = snapshot
		}
		return reflect.ValueOf(value), nil
	default:
		return reflect.Value{}, fmt.Errorf("%w: unsupported input %s", ErrInvalidRule, t)
	}
}

func parseRuleOutputs(outs []reflect.Value) (Verdict, string, error) {
	if len(outs) == 0 {
		return VerdictUnknown, "", nil
	}

	var (
		verdict = VerdictUnknown
		label   string
		err     error
	)
	for i, out := range outs {
		t := out.Type()
		if t.Implements(errorType) {
			if !isNilable(out.Kind()) || !out.IsNil() {
				err = out.Interface().(error)
			}
			continue
		}
		switch {
		case t == boolType:
			if i == 0 {
				if out.Bool() {
					verdict = VerdictMalicious
				} else {
					verdict = VerdictBenign
				}
			}
		case t == verdictRT:
			if i == 0 {
				verdict = Verdict(out.Uint())
			}
		case t == intType:
			if i == 0 {
				switch out.Int() {
				case 0:
					verdict = VerdictUnknown
				case 1:
					verdict = VerdictBenign
				case 2:
					verdict = VerdictMalicious
				}
			}
		case t == stringType:
			label = out.String()
		}
	}
	return verdict, label, err
}

func isNilable(kind reflect.Kind) bool {
	switch kind {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return true
	default:
		return false
	}
}
