package netsec

import (
	"errors"
	"strings"
)

// AllUnnamed is the target token of an --add-opens entry that stands for
// every unnamed module (the command-line equivalent of "ALL-UNNAMED").
const AllUnnamed = "ALL-UNNAMED"

// RequireDirective describes a single `requires` directive of a module.
type RequireDirective struct {
	// Module is the name of the required module.
	Module string
	// Transitive marks a `requires public`/`requires transitive` edge whose
	// readability is propagated to every module reading this one.
	Transitive bool
	// Static marks a `requires static` edge. Static edges still establish
	// ordinary readability while the required module is present.
	Static bool
}

// PackageDirective describes an `exports` or `opens` directive.
// An empty Targets list means the package is exported/opened unqualified to
// every module; a non-empty list restricts it to the named friends.
type PackageDirective struct {
	Package string
	Targets []string
}

// ModuleDescriptor is the subset of a java.lang.module.ModuleDescriptor that
// the reflection-openness decision depends on.
type ModuleDescriptor struct {
	Name      string
	Open      bool
	Automatic bool
	Requires  []RequireDirective
	Exports   []PackageDirective
	Opens     []PackageDirective
}

// AddOpensPatch models one startup --add-opens=<module>/<package>=<target>
// patch. Target is AllUnnamed when the patch targets the unnamed module.
type AddOpensPatch struct {
	Module  string
	Package string
	Target  string
}

// ParseAddOpens parses one --add-opens argument as accepted by the JVM.
// It tolerates the leading "--add-opens" token and both the space-separated
// ("--add-opens mod/p=TARGET") and equals-separated ("--add-opens=mod/p=TARGET")
// command-line spellings.
func ParseAddOpens(arg string) (AddOpensPatch, error) {
	arg = strings.TrimSpace(arg)
	arg = strings.TrimPrefix(arg, "--add-opens=")
	arg = strings.TrimPrefix(arg, "--add-opens ")
	arg = strings.TrimSpace(arg)

	eq := strings.IndexByte(arg, '=')
	if eq <= 0 || eq == len(arg)-1 {
		return AddOpensPatch{}, errors.New("netsec: invalid --add-opens, want <module>/<package>=<target>: " + arg)
	}
	source := arg[:eq]
	target := arg[eq+1:]

	slash := strings.IndexByte(source, '/')
	if slash <= 0 || slash == len(source)-1 {
		return AddOpensPatch{}, errors.New("netsec: invalid --add-opens source, want <module>/<package>: " + source)
	}

	return AddOpensPatch{
		Module:  source[:slash],
		Package: source[slash+1:],
		Target:  target,
	}, nil
}

// ParseAddOpensAll parses a batch of --add-opens arguments, stopping at the
// first malformed entry.
func ParseAddOpensAll(args ...string) ([]AddOpensPatch, error) {
	patches := make([]AddOpensPatch, 0, len(args))
	for _, arg := range args {
		patch, err := ParseAddOpens(arg)
		if err != nil {
			return nil, err
		}
		patches = append(patches, patch)
	}
	return patches, nil
}
