package netsec

// checkFuncCall enforces the static template-function policy for one call.
func checkFuncCall(call *FuncCall, forbidden map[string]struct{}) []Violation {
	if _, banned := forbidden[call.Name]; banned {
		return []Violation{{
			Kind:     ViolationForbiddenFunction,
			Message:  "function " + call.Name + " reads external state and is forbidden in chart templates",
			Function: call.Name,
			Position: call.Position,
		}}
	}

	if call.Name != "lookup" {
		return nil
	}

	// lookup(apiVersion, kind, namespace, name) — kind is argument 1.
	if len(call.Args) < 2 {
		return []Violation{{
			Kind:     ViolationDynamicLookup,
			Message:  "lookup call has no kind argument and cannot be statically resolved",
			Function: call.Name,
			Position: call.Position,
		}}
	}

	kindArg := call.Args[1]
	if !kindArg.Static {
		return []Violation{{
			Kind:     ViolationDynamicLookup,
			Message:  "lookup kind argument is dynamic (" + kindArg.Value + ") and cannot be statically resolved",
			Function: call.Name,
			Position: call.Position,
		}}
	}

	if equalFold(kindArg.Value, "Secret") {
		return []Violation{{
			Kind:     ViolationLookupSecret,
			Message:  "lookup must not read cluster Secrets; kind=\"Secret\"",
			Function: call.Name,
			Position: call.Position,
		}}
	}

	return nil
}
