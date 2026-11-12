package netsec

import (
	"fmt"
	"path"
	"strings"
)

// PathRule grants Allowed on Path and, when Dir is true, on everything
// beneath Path. The type is explicit because a compositional model cannot
// open the target file descriptor to discover whether Path is a directory.
// Path must be absolute; it is cleaned with path.Clean during validation.
type PathRule struct {
	Path    string
	Dir     bool
	Allowed AccessFS
}

// Layer is one Landlock ruleset in the stack. Handled is the ruleset's
// handled_access_fs mask: every right it contains is governed by this layer.
// A governed right that none of the layer's matching rules grants is denied,
// including when Rules is empty or contains no matching path.
type Layer struct {
	Handled AccessFS
	Rules   []PathRule
}

// Profile is a validated, immutable stack of Landlock layers targeting one
// kernel ABI. Build profiles with Compose.
type Profile struct {
	abi     ABI
	layers  []compiledLayer
	handled AccessFS
}

type compiledLayer struct {
	handled AccessFS
	rules   []compiledRule
}

type compiledRule struct {
	dir     bool
	path    string
	allowed AccessFS
}

// UnsupportedAccessError reports rights that the target kernel ABI cannot
// represent. Callers must refuse to start the sandbox in this case: dropping
// the bits would leave those actions unrestricted.
type UnsupportedAccessError struct {
	ABI           ABI
	Supported     AccessFS
	Requested     AccessFS
	Unsupported   AccessFS
	LayerIndex    int
	InPathRule    bool
	PathRuleIndex int
}

func (e *UnsupportedAccessError) Error() string {
	where := fmt.Sprintf("layer %d handled_access_fs", e.LayerIndex)
	if e.InPathRule {
		where = fmt.Sprintf("layer %d path rule %d", e.LayerIndex, e.PathRuleIndex)
	}
	return fmt.Sprintf(
		"landlock: %s requests unsupported rights %s for %s; refusing to degrade (supported: %s)",
		where, e.Unsupported, e.ABI, e.Supported,
	)
}

// InvalidConfigError reports a configuration that the kernel would reject or
// that does not describe a coherent ruleset.
type InvalidConfigError struct {
	ABI        ABI
	LayerIndex int
	Reason     string
}

func (e *InvalidConfigError) Error() string {
	return fmt.Sprintf("landlock: invalid configuration in layer %d for %s: %s",
		e.LayerIndex, e.ABI, e.Reason)
}

// referCompanionRights mirrors the kernel check in
// landlock_create_ruleset(): a ruleset handling REFER must also handle the
// rights needed to perform the underlying link/rename operations, otherwise
// the kernel rejects the ruleset with EINVAL.
const referCompanionRights = AccessFSRemoveDir |
	AccessFSRemoveFile |
	AccessFSMakeDir |
	AccessFSMakeReg

// Compose validates layers against abi and returns a ready-to-query Profile.
// It fails closed: any handled or rule-level right unknown to abi produces an
// UnsupportedAccessError instead of being masked off.
func Compose(abi ABI, layers []Layer) (*Profile, error) {
	supported := abi.SupportedAccessFS()
	if supported == 0 {
		return nil, &InvalidConfigError{
			ABI:        abi,
			LayerIndex: -1,
			Reason:     fmt.Sprintf("unsupported ABI version %d", abi),
		}
	}

	profile := &Profile{abi: abi}
	for layerIndex, layer := range layers {
		if layer.Handled == 0 {
			return nil, &InvalidConfigError{
				ABI:        abi,
				LayerIndex: layerIndex,
				Reason:     "layer handles no filesystem access rights",
			}
		}

		if unsupported := layer.Handled &^ supported; unsupported != 0 {
			return nil, &UnsupportedAccessError{
				ABI:         abi,
				Supported:   supported,
				Requested:   layer.Handled,
				Unsupported: unsupported,
				LayerIndex:  layerIndex,
			}
		}

		if layer.Handled.Contains(AccessFSRefer) &&
			!layer.Handled.Contains(referCompanionRights) {
			missing := referCompanionRights &^ layer.Handled
			return nil, &InvalidConfigError{
				ABI:        abi,
				LayerIndex: layerIndex,
				Reason: fmt.Sprintf(
					"REFER requires handling REMOVE_DIR|REMOVE_FILE|MAKE_DIR|MAKE_REG, missing %s",
					missing,
				),
			}
		}

		compiled := compiledLayer{handled: layer.Handled}
		for ruleIndex, rule := range layer.Rules {
			if rule.Allowed&^supported != 0 {
				return nil, &UnsupportedAccessError{
					ABI:           abi,
					Supported:     supported,
					Requested:     rule.Allowed,
					Unsupported:   rule.Allowed &^ supported,
					LayerIndex:    layerIndex,
					InPathRule:    true,
					PathRuleIndex: ruleIndex,
				}
			}
			if extra := rule.Allowed &^ layer.Handled; extra != 0 {
				return nil, &InvalidConfigError{
					ABI:        abi,
					LayerIndex: layerIndex,
					Reason: fmt.Sprintf(
						"path rule %q grants %s which the layer does not handle",
						rule.Path, extra,
					),
				}
			}

			cleanPath, ok := cleanAbsolutePath(rule.Path)
			if !ok {
				return nil, &InvalidConfigError{
					ABI:        abi,
					LayerIndex: layerIndex,
					Reason:     fmt.Sprintf("path rule %q is not an absolute, cleanable path", rule.Path),
				}
			}

			compiled.rules = append(compiled.rules, compiledRule{
				dir:     rule.Dir || cleanPath == "/",
				path:    cleanPath,
				allowed: rule.Allowed,
			})
		}

		profile.layers = append(profile.layers, compiled)
		profile.handled |= layer.Handled
	}

	return profile, nil
}

// Effective is the result of evaluating a path against every stacked layer.
type Effective struct {
	// Path is the cleaned, absolute target path.
	Path string
	// Handled is the union of every layer's handled mask. Rights outside it
	// are governed by no layer and therefore remain unrestricted.
	Handled AccessFS
	// Allowed is the effective grant at Path after the intersection of all
	// layers and all matching ancestor/self rules.
	Allowed AccessFS
	// Denied is the subset of Handled that no stack of layers jointly grants.
	Denied AccessFS
	// Ungoverned is the set of known rights (per ABI) that no layer handles.
	Ungoverned AccessFS
}

// Allows reports whether right is both governed and granted at Path.
func (e Effective) Allows(right AccessFS) bool {
	return e.Allowed.Contains(right)
}

// IsDenied reports whether right is governed but effectively denied at Path.
func (e Effective) IsDenied(right AccessFS) bool {
	return e.Handled.Contains(right) && !e.Allowed.Contains(right)
}

// EffectiveAccess computes the rights that hold at target after all layers
// are applied.
//
// Within each layer the matching rules are unioned (a child path matches both
// its own rules and every ancestor directory rule). Across layers the results
// are intersected: a right is allowed only if every layer that handles it
// grants it. A layer handling a right without any matching rule denies it.
func (p *Profile) EffectiveAccess(target string) (Effective, error) {
	cleanTarget, ok := cleanAbsolutePath(target)
	if !ok {
		return Effective{}, &InvalidConfigError{
			ABI:        p.abi,
			LayerIndex: -1,
			Reason:     fmt.Sprintf("target path %q is not an absolute, cleanable path", target),
		}
	}

	allowed := p.handled
	for _, layer := range p.layers {
		var granted AccessFS
		for _, rule := range layer.rules {
			if rule.matches(cleanTarget) {
				granted |= rule.allowed
			}
		}
		// A governed right absent from the union of matching rules is denied
		// by this layer; rights the layer does not handle pass through.
		allowed &^= layer.handled &^ granted
	}

	allowed &= p.handled
	return Effective{
		Path:       cleanTarget,
		Handled:    p.handled,
		Allowed:    allowed,
		Denied:     p.handled &^ allowed,
		Ungoverned: p.abi.SupportedAccessFS() &^ p.handled,
	}, nil
}

// ABI returns the profile's target kernel ABI.
func (p *Profile) ABI() ABI { return p.abi }

// Handled returns the union of every layer's handled mask.
func (p *Profile) Handled() AccessFS { return p.handled }

// LayerCount returns the number of validated layers in the stack.
func (p *Profile) LayerCount() int { return len(p.layers) }

func (r compiledRule) matches(target string) bool {
	if r.path == target {
		return true
	}
	if !r.dir {
		return false
	}
	if r.path == "/" {
		return true
	}
	return strings.HasPrefix(target, r.path+"/")
}

func cleanAbsolutePath(value string) (string, bool) {
	if value == "" || !path.IsAbs(value) {
		return "", false
	}
	cleaned := path.Clean(value)
	if cleaned != "/" && cleaned[len(cleaned)-1] == '/' {
		return "", false
	}
	return cleaned, true
}
