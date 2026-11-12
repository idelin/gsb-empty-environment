package netsec

import (
	"fmt"
	"strings"
)

// Config contains the rsyncd modules that requests may address.
//
// Modules is the normal representation. ModuleList is accepted as a
// convenience for callers that already have a slice of modules. It must not
// contain two modules with the same Name.
type Config struct {
	Modules    map[string]Module
	ModuleList []Module
	// Global contains parameters that appeared before the first module (or in
	// a [global] section). ParseConfig copies these defaults into each module.
	Global Module
}

// Module is the security-relevant part of one rsyncd.conf module.
//
// Root (or Path, as a compatibility alias) is the module's configured path.
// ReadOnly and UseChroot mirror the rsyncd.conf parameters of the same names.
// For configs read by ParseConfig, rsync's defaults (read only and use chroot
// both true) are applied. In a programmatically built Module, the zero value
// means writable and no chroot; set ReadOnly or UseChroot explicitly when the
// corresponding restriction is needed.
type Module struct {
	Name string

	// Root is the directory to which request paths are confined.
	Root string
	// Path is an alias for Root, named after rsyncd.conf's "path" parameter.
	Path string

	HostsAllow []string
	HostsDeny  []string

	ReadOnly  bool
	UseChroot bool

	RefuseOptions []string
	Exclude       []string
}

// Request describes one operation on a module.
//
// Host is the client host or IP address. Upload (or the Write alias) marks a
// client-to-daemon write operation. Options are rsync command-line options
// with or without their leading dashes.
type Request struct {
	Module string
	Host   string
	Path   string

	Options []string
	Args    []string

	Upload bool
	Write  bool

	// ClientAddr is accepted as an alias for Host.
	ClientAddr string
}

// Reason identifies why a request was allowed or denied.
type Reason string

const (
	ReasonAllowed       Reason = ""
	ReasonUnknownModule Reason = "unknown module"
	ReasonHostDenied    Reason = "host denied"
	ReasonOptionRefused Reason = "refused option"
	ReasonBadPath       Reason = "invalid path"
	ReasonPathEscape    Reason = "path escapes module root"
	ReasonSymlinkEscape Reason = "symlink escapes module root"
	ReasonExcluded      Reason = "path excluded"
	ReasonReadOnly      Reason = "module is read only"
)

// Decision is the result of evaluating a request.
type Decision struct {
	Allowed bool
	Reason  Reason
	Detail  string
}

// AdmissionResult is an alias for Decision.
type AdmissionResult = Decision

// Err converts a denied decision into an error. It returns nil when the
// request is allowed.
func (d Decision) Err() error {
	if d.Allowed {
		return nil
	}
	return &DeniedError{Reason: d.Reason, Detail: d.Detail}
}

// DeniedError is returned by Check and the error-returning admission helpers.
type DeniedError struct {
	Reason Reason
	Detail string
}

func (e *DeniedError) Error() string {
	if e.Detail == "" {
		if e.Reason == "" {
			return "request denied"
		}
		return string(e.Reason)
	}
	if e.Reason == "" {
		return e.Detail
	}
	return fmt.Sprintf("%s: %s", e.Reason, e.Detail)
}

// Evaluate checks one request against the supplied configuration.
func Evaluate(cfg Config, req Request) Decision {
	module, ok := lookupModule(cfg, req.Module)
	if !ok {
		return deny(ReasonUnknownModule, req.Module)
	}

	if !hostAllowed(module, requestHost(req)) {
		return deny(ReasonHostDenied, requestHost(req))
	}

	options := append([]string(nil), req.Options...)
	options = append(options, req.Args...)
	if option, refused := refusedOption(module.RefuseOptions, options); refused {
		return deny(ReasonOptionRefused, option)
	}

	cleanPath, err := normalizeRelativePath(req.Path)
	if err != nil {
		return deny(ReasonBadPath, err.Error())
	}

	if excludedBy(module.Exclude, cleanPath) {
		return deny(ReasonExcluded, cleanPath)
	}

	if (req.Upload || req.Write) && module.ReadOnly {
		return deny(ReasonReadOnly, req.Module)
	}

	root := moduleRoot(module)
	if root == "" {
		// The module root itself is not a path outside the module. Keep host,
		// option, exclusion, and read-only checks usable for configs that only
		// describe those policies, but fail closed for any real entry.
		if cleanPath != "." {
			return deny(ReasonBadPath, "module has no configured root")
		}
	} else {
		if _, err := securePath(root, cleanPath, module.UseChroot); err != nil {
			if pathErrorIsSymlink(err) {
				return deny(ReasonSymlinkEscape, err.Error())
			}
			return deny(ReasonPathEscape, err.Error())
		}
		if !module.UseChroot {
			if err := checkOriginalSymlinks(root, req.Path); err != nil {
				return deny(ReasonSymlinkEscape, err.Error())
			}
		}
	}

	return Decision{Allowed: true}
}

// EvaluateRequest is an alias for Evaluate.
func EvaluateRequest(cfg Config, req Request) Decision {
	return Evaluate(cfg, req)
}

// EvaluateConfig is an alias for Evaluate for callers holding a *Config.
func EvaluateConfig(cfg *Config, req Request) Decision {
	if cfg == nil {
		return deny(ReasonUnknownModule, req.Module)
	}
	return Evaluate(*cfg, req)
}

// Evaluate evaluates a request as a method on a configuration.
func (cfg Config) Evaluate(req Request) Decision {
	return Evaluate(cfg, req)
}

// Evaluate checks a request against this module alone.
func (module Module) Evaluate(req Request) Decision {
	if req.Module == "" {
		req.Module = module.Name
	}
	if module.Name == "" {
		module.Name = req.Module
	}
	return Evaluate(Config{Modules: map[string]Module{module.Name: module}}, req)
}

// Check returns nil if the request is admitted and a *DeniedError otherwise.
func Check(cfg Config, req Request) error {
	return Evaluate(cfg, req).Err()
}

// CheckRequest is an alias for Check.
func CheckRequest(cfg Config, req Request) error {
	return Check(cfg, req)
}

// Admit is an alias for Check.
func Admit(cfg Config, req Request) error {
	return Check(cfg, req)
}

// Authorize is an alias for Check.
func Authorize(cfg Config, req Request) error {
	return Check(cfg, req)
}

// IsAllowed reports whether the request is admitted.
func IsAllowed(cfg Config, req Request) bool {
	return Evaluate(cfg, req).Allowed
}

func deny(reason Reason, detail string) Decision {
	return Decision{Reason: reason, Detail: detail}
}

func lookupModule(cfg Config, name string) (Module, bool) {
	if name == "" {
		return Module{}, false
	}

	if module, ok := cfg.Modules[name]; ok {
		return module, true
	}

	var found Module
	count := 0
	for _, module := range cfg.ModuleList {
		if module.Name == name {
			found = module
			count++
		}
	}
	if count == 1 {
		return found, true
	}
	return Module{}, false
}

func moduleRoot(module Module) string {
	if strings.TrimSpace(module.Root) != "" {
		return module.Root
	}
	return module.Path
}

func requestHost(req Request) string {
	if strings.TrimSpace(req.Host) != "" {
		return req.Host
	}
	return req.ClientAddr
}
