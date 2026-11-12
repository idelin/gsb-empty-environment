package netsec

import "errors"

// Analyze resolves every DT_NEEDED entry and every supplied
// /etc/ld.so.preload entry, then checks the selected path and all earlier
// search locations for attacker-controlled directories.
//
// The result models secure-execution mode: LD_LIBRARY_PATH and LD_PRELOAD are
// not considered. DT_RPATH is ignored when DT_RUNPATH is present. The
// remaining order is RPATH or RUNPATH, ld.so.cache, then DefaultDirs.
func Analyze(elfPath string, opts Options) (*Report, error) {
	if opts.Files == nil {
		return nil, errors.New("netsec: Options.Files is required")
	}
	if elfPath == "" {
		return nil, errors.New("netsec: empty ELF path")
	}

	meta, err := ReadELF(elfPath)
	if err != nil {
		return nil, err
	}
	a := newAnalyzer(opts, elfPath)
	report := &Report{
		ELFPath: elfPath,
		Origin:  a.origin,
		Safe:    true,
	}

	for _, name := range meta.Needed {
		dependency := a.resolve(name, NeededEntry, meta)
		report.Needed = append(report.Needed, dependency)
		if !dependency.Safe {
			report.Safe = false
		}
	}
	for _, name := range opts.Preload {
		dependency := a.resolve(name, PreloadEntry, meta)
		report.Preload = append(report.Preload, dependency)
		if !dependency.Safe {
			report.Safe = false
		}
	}
	return report, nil
}
