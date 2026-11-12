package admission

// OutputFile is one file output referenced by an ActionResult, mirroring the
// Digest + path fields of build.bazel.remote.execution.v2.OutputFile.
type OutputFile struct {
	Path   string
	Digest Digest
}

// OutputDirectory is one directory output; its tree digest must also be
// present in CAS before the ActionResult may be admitted.
type OutputDirectory struct {
	Path       string
	TreeDigest Digest
}

// ActionResult is the transport-neutral subset of
// build.bazel.remote.execution.v2.ActionResult that the guard validates.
// Stdout/stderr blobs and every output file/directory digest must already
// exist in CAS with matching sizes.
type ActionResult struct {
	OutputFiles       []OutputFile
	OutputDirectories []OutputDirectory
	StdoutDigest      *Digest
	StderrDigest      *Digest
	ExitCode          int32
}

// ReferencedDigests returns the de-duplicated set of CAS digests the result
// depends on, in a stable order (files, directories, stdout, stderr).
func (r *ActionResult) ReferencedDigests() []Digest {
	seen := map[Digest]bool{}
	var out []Digest
	add := func(d Digest) {
		if seen[d] {
			return
		}
		seen[d] = true
		out = append(out, d)
	}
	for _, f := range r.OutputFiles {
		add(f.Digest)
	}
	for _, d := range r.OutputDirectories {
		add(d.TreeDigest)
	}
	if r.StdoutDigest != nil {
		add(*r.StdoutDigest)
	}
	if r.StderrDigest != nil {
		add(*r.StderrDigest)
	}
	return out
}

// Equal reports whether two admitted results reference the exact same set
// of outputs. A re-upload of an equivalent result is idempotent and allowed;
// any difference is a poisoning attempt and must be rejected.
func (r *ActionResult) Equal(other *ActionResult) bool {
	if other == nil {
		return false
	}
	if r.ExitCode != other.ExitCode || len(r.OutputFiles) != len(other.OutputFiles) ||
		len(r.OutputDirectories) != len(other.OutputDirectories) {
		return false
	}
	for i := range r.OutputFiles {
		if r.OutputFiles[i] != other.OutputFiles[i] {
			return false
		}
	}
	for i := range r.OutputDirectories {
		if r.OutputDirectories[i] != other.OutputDirectories[i] {
			return false
		}
	}
	if !digestPtrEqual(r.StdoutDigest, other.StdoutDigest) ||
		!digestPtrEqual(r.StderrDigest, other.StderrDigest) {
		return false
	}
	return true
}

func digestPtrEqual(a, b *Digest) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
