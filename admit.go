package netsec

// Options controls the admission decision.
type Options struct {
	// MinSecretLen is the minimum decoded secret length that participates
	// in leak detection. Values shorter than this are ignored because
	// short tokens produce excessive false positives.
	MinSecretLen int
	// ForbiddenFunctions lists template functions that read external
	// state and are rejected on sight. nil uses the built-in set.
	ForbiddenFunctions []string
}

// DefaultMinSecretLen is the minimum secret length applied when
// Options.MinSecretLen is unset.
const DefaultMinSecretLen = 6

func defaultForbiddenFunctions() map[string]struct{} {
	return map[string]struct{}{
		"getHostByName": {},
		"env":           {},
		"expandenv":     {},
		"readFile":      {},
	}
}

// Admit evaluates rendered chart output before it is allowed into the
// cluster. It performs two independent checks:
//
//   - static checks of every template function call (lookup targeting
//     Secrets, lookup with a non-static kind, and any forbidden
//     external-state function);
//   - data checks of every non-Secret rendered resource against the
//     cluster Secret values returned by fetchSecrets.
//
// Admit fails closed: if Secret values are required and fetchSecrets is
// nil or returns an error, Admit returns a non-nil error and no Report.
func Admit(calls []FuncCall, resources []Resource, fetchSecrets SecretFetcher, opts Options) (*Report, error) {
	minLen := opts.MinSecretLen
	if minLen <= 0 {
		minLen = DefaultMinSecretLen
	}
	forbidden := defaultForbiddenFunctions()
	for _, name := range opts.ForbiddenFunctions {
		forbidden[name] = struct{}{}
	}

	report := &Report{Allowed: true}

	for i := range calls {
		report.Violations = append(report.Violations, checkFuncCall(&calls[i], forbidden)...)
	}

	var secrets []SecretValue
	if hasNonSecretResource(resources) {
		if fetchSecrets == nil {
			return nil, fmtError("netsec: secret value fetcher is required when non-Secret resources are rendered")
		}
		values, err := fetchSecrets()
		if err != nil {
			return nil, fmtError("netsec: fetching cluster Secret values failed: " + err.Error())
		}
		secrets = values
	}

	forms := buildSecretForms(secrets, minLen)
	for i := range resources {
		if isSecretKind(resources[i].Kind) {
			continue
		}
		report.Violations = append(report.Violations, scanResource(&resources[i], forms)...)
	}

	if len(report.Violations) > 0 {
		report.Allowed = false
		sortViolations(report.Violations)
	}
	return report, nil
}

func hasNonSecretResource(resources []Resource) bool {
	for i := range resources {
		if !isSecretKind(resources[i].Kind) {
			return true
		}
	}
	return false
}

func isSecretKind(kind string) bool {
	return equalFold(kind, "Secret")
}
