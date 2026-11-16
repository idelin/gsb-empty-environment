// Package renderguard implements render-time admission checks for chart
// output. It rejects templates that read cluster Secrets or external state
// through template functions, and rejects rendered non-Secret resources
// whose string fields leak known Secret values in plain, base64, or hex
// form.
package renderguard

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// MinSecretLength is the minimum length of a Secret value that is
// considered when scanning rendered output. Shorter values are ignored to
// avoid false positives on common short strings.
const MinSecretLength = 6

// CallArg is a single argument of a template function call.
type CallArg struct {
	// Value is the literal argument text.
	Value string
	// Literal is false when the argument is dynamically composed and
	// cannot be determined statically.
	Literal bool
}

// FunctionCall describes one function invocation found in a template.
type FunctionCall struct {
	Name string
	Args []CallArg
}

// StringField is one string value of a rendered resource, identified by a
// field path such as "data.config" or "metadata.annotations.team".
type StringField struct {
	Path  string
	Value string
}

// Resource is a rendered manifest.
type Resource struct {
	Kind      string
	Namespace string
	Name      string
	Fields    []StringField
}

// Input bundles everything the admission check needs.
type Input struct {
	Calls     []FunctionCall
	Resources []Resource
}

// SecretLookup returns the Secret values of one namespace, keyed by Secret
// name. It is supplied by the caller and is only invoked for namespaces
// that contain rendered non-Secret resources.
type SecretLookup func(namespace string) (map[string][]string, error)

// Reason classifies a Finding.
type Reason string

const (
	ReasonLookupSecret      Reason = "lookup-secret"
	ReasonLookupKindUnknown Reason = "lookup-kind-undetermined"
	ReasonDangerousFunction Reason = "dangerous-function"
	ReasonSecretValueLeak   Reason = "secret-value-leak"
)

// Finding describes one rejected construct. Kind, Namespace, and Name
// identify the offending resource for leak findings and are empty for
// template-function findings. Field is the field path of a leaked value.
type Finding struct {
	Reason    Reason
	Message   string
	Kind      string
	Namespace string
	Name      string
	Field     string
}

// Decision is the outcome of an admission evaluation.
type Decision struct {
	Allowed  bool
	Findings []Finding
}

// dangerousFunctions read external state at render time and are always
// rejected.
var dangerousFunctions = map[string]bool{
	"getHostByName": true,
	"env":           true,
	"expandenv":     true,
	"readFile":      true,
}

// Evaluate runs all admission checks and reports every violation found.
func Evaluate(in Input, lookupSecrets SecretLookup) (Decision, error) {
	findings := checkCalls(in.Calls)
	leaks, err := checkResources(in.Resources, lookupSecrets)
	if err != nil {
		return Decision{}, err
	}
	findings = append(findings, leaks...)
	return Decision{Allowed: len(findings) == 0, Findings: findings}, nil
}

func checkCalls(calls []FunctionCall) []Finding {
	var findings []Finding
	for _, call := range calls {
		if dangerousFunctions[call.Name] {
			findings = append(findings, Finding{
				Reason:  ReasonDangerousFunction,
				Message: fmt.Sprintf("function %q reads external state at render time and is not allowed", call.Name),
			})
			continue
		}
		if call.Name != "lookup" {
			continue
		}
		kind, ok := lookupKind(call)
		switch {
		case !ok:
			findings = append(findings, Finding{
				Reason:  ReasonLookupKindUnknown,
				Message: "lookup kind argument is not a static literal and cannot be verified",
			})
		case strings.EqualFold(kind, "Secret"):
			findings = append(findings, Finding{
				Reason:  ReasonLookupSecret,
				Message: "lookup must not read Secret objects from the cluster",
			})
		}
	}
	return findings
}

// lookupKind extracts the kind argument of a Helm-style
// "lookup apiVersion kind namespace name" call.
func lookupKind(call FunctionCall) (string, bool) {
	if len(call.Args) < 2 {
		return "", false
	}
	kind := call.Args[1]
	if !kind.Literal || kind.Value == "" {
		return "", false
	}
	return kind.Value, true
}

func checkResources(resources []Resource, lookupSecrets SecretLookup) ([]Finding, error) {
	secretsByNS := map[string][]string{}
	loaded := map[string]bool{}
	var findings []Finding
	for _, res := range resources {
		if strings.EqualFold(res.Kind, "Secret") {
			continue
		}
		if !loaded[res.Namespace] {
			if lookupSecrets == nil {
				return nil, errors.New("renderguard: SecretLookup callback is required to scan rendered resources")
			}
			secrets, err := lookupSecrets(res.Namespace)
			if err != nil {
				return nil, fmt.Errorf("renderguard: looking up secrets in namespace %q: %w", res.Namespace, err)
			}
			secretsByNS[res.Namespace] = flattenSecretValues(secrets)
			loaded[res.Namespace] = true
		}
		for _, field := range res.Fields {
			for _, variant := range leakVariants(secretsByNS[res.Namespace]) {
				if strings.Contains(field.Value, variant) {
					findings = append(findings, Finding{
						Reason:    ReasonSecretValueLeak,
						Message:   "field contains a cluster Secret value or its base64/hex encoding",
						Kind:      res.Kind,
						Namespace: res.Namespace,
						Name:      res.Name,
						Field:     field.Path,
					})
					break
				}
			}
		}
	}
	return findings, nil
}

// flattenSecretValues returns the distinct secret values that are long
// enough to scan for, sorted for deterministic evaluation.
func flattenSecretValues(secrets map[string][]string) []string {
	seen := map[string]bool{}
	var values []string
	for _, list := range secrets {
		for _, v := range list {
			if len(v) < MinSecretLength || seen[v] {
				continue
			}
			seen[v] = true
			values = append(values, v)
		}
	}
	sort.Strings(values)
	return values
}

// leakVariants expands secret values into the plain, base64, and hex forms
// that rendered output may carry.
func leakVariants(values []string) []string {
	seen := map[string]bool{}
	var variants []string
	add := func(s string) {
		if len(s) >= MinSecretLength && !seen[s] {
			seen[s] = true
			variants = append(variants, s)
		}
	}
	for _, v := range values {
		add(v)
		encoded := base64.StdEncoding.EncodeToString([]byte(v))
		add(encoded)
		add(strings.TrimRight(encoded, "="))
		lowerHex := hex.EncodeToString([]byte(v))
		add(lowerHex)
		add(strings.ToUpper(lowerHex))
	}
	return variants
}
