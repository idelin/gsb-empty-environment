package netsec

// Version identifies the analysis model implemented by this package.
const Version = "1.0"

// Check is the concrete, non-generic entry point intended for hosts that load
// this package dynamically or call it through an integration adapter.
func Check(p Program, entryParam int64, options ...Options) Result {
	result, _ := analyze(p, entryParam, options...)
	return result
}

// AnalyzeProgram is an alias for Check with a name matching the data type.
func AnalyzeProgram(p Program, entryParam int64, options ...Options) Result {
	return Check(p, entryParam, options...)
}

// AnalyzeWithOptions is an alias for Check for hosts that pass options
// explicitly.
func AnalyzeWithOptions(p Program, entryParam int64, options Options) Result {
	return Check(p, entryParam, options)
}

// AnalyzeNamed runs the program with an explicit entry function. The entry
// stored in the Program is ignored.
func AnalyzeNamed(p Program, entry string, entryParam int64, options ...Options) Result {
	p.Entry = entry
	return Check(p, entryParam, options...)
}
