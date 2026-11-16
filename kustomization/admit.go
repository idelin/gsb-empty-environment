package kustomization

import (
	"path"
	"strings"
)

// Admit evaluates one admission request and returns an *AdmissionError listing
// every rejected construct. A nil result means the kustomization is allowed.
func Admit(req *Request) error {
	if req == nil || req.Config == nil {
		return &AdmissionError{Violations: []Violation{{
			Field:  "request",
			Reason: "missing parsed kustomization",
		}}}
	}

	entry := normalizeRepoPath(req.Entry)
	if entry == "" {
		return &AdmissionError{Violations: []Violation{{
			Field:  "request.entry",
			Reason: "entry kustomization path is required",
		}}}
	}

	idx := newFileIndex(req.Files, req.RootDir)
	hosts := make(map[string]bool, len(req.AllowedHosts))
	for _, host := range req.AllowedHosts {
		hosts[strings.ToLower(strings.TrimSpace(host))] = true
	}

	configs := make(map[string]*Kustomization, len(req.Configs)+1)
	for p, kc := range req.Configs {
		if kc != nil {
			configs[normalizeRepoPath(p)] = kc
		}
	}
	configs[entry] = req.Config

	nodes := make(map[string]string, len(configs))
	for configPath := range configs {
		nodes[configPath] = normalizeRepoPath(path.Dir(configPath))
	}

	var violations []Violation
	for configPath, kc := range configs {
		baseDir := path.Dir(configPath)
		violations = append(violations, checkReferenceEntries(kc.Resources, "resources", baseDir, idx, hosts, false)...)
		violations = append(violations, checkReferenceEntries(kc.Bases, "bases", baseDir, idx, hosts, false)...)
		violations = append(violations, checkReferenceEntries(kc.Patches, "patches", baseDir, idx, hosts, false)...)
		violations = append(violations, checkReferenceEntries(kc.Generators, "generators", baseDir, idx, hosts, true)...)
		violations = append(violations, checkReferenceEntries(kc.Transformers, "transformers", baseDir, idx, hosts, true)...)
		violations = append(violations, checkHelmCharts(kc.HelmCharts, hosts)...)
		violations = append(violations, checkConfigMapGenerator(kc.ConfigMapGenerator, baseDir, idx)...)
	}

	violations = append(violations, detectCycles(nodes, configs, idx, hosts)...)
	if len(violations) == 0 {
		return nil
	}
	return &AdmissionError{Violations: violations}
}

// checkHelmCharts requires an https allowlisted repository and an explicit
// chart version so builds cannot float with repository changes.
func checkHelmCharts(charts []HelmChart, hosts map[string]bool) []Violation {
	var violations []Violation
	for i, chart := range charts {
		field := "helmCharts[" + itoa(i) + "]"
		if strings.TrimSpace(chart.Repo) == "" {
			violations = append(violations, Violation{Field: field, Reason: "repo is required"})
			continue
		}
		if strings.TrimSpace(chart.Version) == "" {
			violations = append(violations, Violation{Field: field, Value: chart.Repo, Reason: "version is required"})
		}
		violations = append(violations, checkHelmRepo(chart.Repo, field, hosts)...)
	}
	return violations
}

// checkConfigMapGenerator applies the repository-root path policy to every
// configMapGenerator file and env source.
func checkConfigMapGenerator(entries []ConfigMapEntry, baseDir string, idx *fileIndex) []Violation {
	var violations []Violation
	for i, entry := range entries {
		field := "configMapGenerator[" + itoa(i) + "].files"
		for _, ref := range entry.Files {
			if isRemoteReference(ref) {
				violations = append(violations, Violation{Field: field, Value: ref, Reason: "remote files are not allowed"})
				continue
			}
			if _, bad := idx.resolveLocal(baseDir, ref, field); bad != nil {
				violations = append(violations, bad...)
			}
		}
		if entry.Env != "" {
			field := "configMapGenerator[" + itoa(i) + "].env"
			if isRemoteReference(entry.Env) {
				violations = append(violations, Violation{Field: field, Value: entry.Env, Reason: "remote env files are not allowed"})
			} else if _, bad := idx.resolveLocal(baseDir, entry.Env, field); bad != nil {
				violations = append(violations, bad...)
			}
		}
	}
	return violations
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n := i; n > 0; n /= 10 {
		pos--
		buf[pos] = byte('0' + n%10)
	}
	return string(buf[pos:])
}
