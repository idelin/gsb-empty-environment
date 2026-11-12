package netsec

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
)

type analyzer struct {
	opts   Options
	cwd    string
	origin string
}

type searchDir struct {
	raw    string
	path   string
	source SearchSource
	issues []Issue
}

func newAnalyzer(opts Options, elfPath string) *analyzer {
	cwd := opts.WorkingDir
	if cwd == "" {
		cwd = "/"
	}
	origin := opts.Origin
	if origin == "" {
		if path.IsAbs(elfPath) {
			origin = path.Dir(path.Clean(elfPath))
		} else {
			origin = path.Dir(path.Join(cwd, elfPath))
		}
	} else if !path.IsAbs(origin) {
		origin = path.Join(cwd, origin)
	}
	origin = path.Clean(origin)
	return &analyzer{opts: opts, cwd: path.Clean(cwd), origin: origin}
}

func absoluteForAnalysis(name, cwd string) (string, bool) {
	if path.IsAbs(name) {
		return path.Clean(name), true
	}
	if path.IsAbs(cwd) {
		return path.Clean(path.Join(cwd, name)), false
	}
	return path.Clean(name), false
}

func (a *analyzer) pathSearchDirs(meta ELFMetadata) []searchDir {
	var raws []string
	var source SearchSource
	if meta.HasRUNPATH {
		raws = meta.RUNPATH
		source = SourceRUNPATH
	} else {
		raws = meta.RPATH
		source = SourceRPATH
	}

	var dirs []searchDir
	for _, raw := range raws {
		for _, element := range strings.Split(raw, ":") {
			expanded, tokenIssues := a.expandTokens(element)
			absolute, isAbsolute := absoluteForAnalysis(expanded, a.cwd)
			issues := tokenIssues
			if !isAbsolute {
				issues = append(issues, Issue{
					Code:   IssueRelativePath,
					Path:   element,
					Source: source,
					Detail: "relative or empty search path is controlled by the setuid caller's working directory",
				})
			}
			dirs = append(dirs, searchDir{
				raw:    element,
				path:   absolute,
				source: source,
				issues: issues,
			})
		}
	}
	return dirs
}

func (a *analyzer) expandTokens(value string) (string, []Issue) {
	var out strings.Builder
	var issues []Issue
	for i := 0; i < len(value); {
		if value[i] != '$' {
			out.WriteByte(value[i])
			i++
			continue
		}

		start := i
		name, end, ok := tokenAt(value, i)
		if ok {
			if replacement, known := a.tokenValue(name); known {
				out.WriteString(replacement)
				i = end
				continue
			}
			issues = append(issues, Issue{
				Code:   IssueUnresolvedToken,
				Path:   value,
				Detail: fmt.Sprintf("dynamic token $%s has no supplied expansion", name),
			})
		}
		out.WriteByte('$')
		i = start + 1
	}
	return out.String(), issues
}

func tokenAt(value string, dollar int) (string, int, bool) {
	if dollar+1 >= len(value) {
		return "", dollar + 1, false
	}
	next := dollar + 1
	braced := value[next] == '{'
	if braced {
		next++
	}
	start := next
	for next < len(value) {
		c := value[next]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			next++
			continue
		}
		break
	}
	if next == start {
		return "", dollar + 1, false
	}
	if braced {
		if next >= len(value) || value[next] != '}' {
			return "", dollar + 1, false
		}
		name := value[start:next]
		next++
		return name, next, true
	}
	return value[start:next], next, true
}

func (a *analyzer) tokenValue(name string) (string, bool) {
	if name == "ORIGIN" {
		return a.origin, a.origin != ""
	}
	if value, ok := a.opts.TokenValues[name]; ok && value != "" {
		return value, true
	}
	return "", false
}

func (a *analyzer) candidateFromDir(dir searchDir, name string) Candidate {
	full := path.Join(dir.path, name)
	exists, safe, issues := a.evaluateFile(full, dir.source)
	issues = append(issues, dir.issues...)
	issues = dedupeIssues(issues)
	return Candidate{
		Entry:     dir.raw,
		Path:      full,
		Directory: dir.path,
		Source:    dir.source,
		Exists:    exists,
		Safe:      safe && len(dir.issues) == 0,
		Issues:    issues,
	}
}

func (a *analyzer) candidateFromPath(rawPath string, source SearchSource, extra []Issue) Candidate {
	if rawPath == "" {
		issues := append([]Issue(nil), extra...)
		issues = append(issues, Issue{
			Code:   IssueInvalidPath,
			Path:   rawPath,
			Source: source,
			Detail: "empty path cannot name a shared object",
		})
		return Candidate{
			Entry:  rawPath,
			Source: source,
			Safe:   false,
			Issues: dedupeIssues(issues),
		}
	}
	absolute, isAbsolute := absoluteForAnalysis(rawPath, a.cwd)
	issues := append([]Issue(nil), extra...)
	if !isAbsolute {
		issues = append(issues, Issue{
			Code:   IssueRelativePath,
			Path:   rawPath,
			Source: source,
			Detail: "relative library path is controlled by the setuid caller's working directory",
		})
	}
	exists, safe, fileIssues := a.evaluateFile(absolute, source)
	issues = append(issues, fileIssues...)
	issues = dedupeIssues(issues)
	return Candidate{
		Entry:     rawPath,
		Path:      absolute,
		Directory: path.Dir(absolute),
		Source:    source,
		Exists:    exists,
		Safe:      safe && len(extra) == 0 && isAbsolute,
		Issues:    issues,
	}
}

func (a *analyzer) evaluateFile(file string, source SearchSource) (bool, bool, []Issue) {
	dirSafe, issues := a.inspectDirectory(path.Dir(file), source)
	info, exists := a.opts.Files.Lookup(file)
	if !exists {
		return false, dirSafe, issues
	}

	if info.Mode&fs.ModeSymlink != 0 {
		issues = append(issues, Issue{
			Code:   IssueNotRegularFile,
			Path:   file,
			Source: source,
			Detail: "symbolic links must be resolved by the caller before admission",
		})
	} else if !info.Mode.IsRegular() {
		issues = append(issues, Issue{
			Code:   IssueNotRegularFile,
			Path:   file,
			Source: source,
			Detail: fmt.Sprintf("mode %s is not a regular file", info.Mode),
		})
	}
	issues = append(issues, a.permissionIssues(file, info, source)...)
	issues = dedupeIssues(issues)
	return true, dirSafe && len(issues) == 0, issues
}

func (a *analyzer) inspectDirectory(dir string, source SearchSource) (bool, []Issue) {
	if !path.IsAbs(dir) {
		return false, []Issue{{
			Code:   IssueRelativePath,
			Path:   dir,
			Source: source,
			Detail: "relative directory cannot be trusted in secure-execution mode",
		}}
	}

	clean := path.Clean(dir)
	var issues []Issue
	if info, ok := a.opts.Files.Lookup("/"); ok {
		if info.Mode&fs.ModeSymlink != 0 || !info.Mode.IsDir() {
			issues = append(issues, Issue{
				Code:   IssueNotDirectory,
				Path:   "/",
				Source: source,
				Detail: fmt.Sprintf("mode %s is not a directory", info.Mode),
			})
		}
		issues = append(issues, a.permissionIssues("/", info, source)...)
	}

	if clean == "/" {
		issues = dedupeIssues(issues)
		return len(issues) == 0, issues
	}

	prefix := "/"
	for _, element := range strings.Split(strings.TrimPrefix(clean, "/"), "/") {
		prefix = path.Join(prefix, element)
		info, ok := a.opts.Files.Lookup(prefix)
		if !ok {
			// A component absent from the snapshot cannot have been planted
			// by an attacker if its already-checked ancestors are safe.
			break
		}
		if info.Mode&fs.ModeSymlink != 0 {
			issues = append(issues, Issue{
				Code:   IssueNotDirectory,
				Path:   prefix,
				Source: source,
				Detail: "symbolic link in search path must be resolved by the caller",
			})
			break
		}
		if !info.Mode.IsDir() {
			issues = append(issues, Issue{
				Code:   IssueNotDirectory,
				Path:   prefix,
				Source: source,
				Detail: fmt.Sprintf("mode %s is not a directory", info.Mode),
			})
			break
		}
		issues = append(issues, a.permissionIssues(prefix, info, source)...)
		if len(issues) != 0 {
			break
		}
	}
	issues = dedupeIssues(issues)
	return len(issues) == 0, issues
}

func (a *analyzer) permissionIssues(name string, info FileInfo, source SearchSource) []Issue {
	var issues []Issue
	perm := info.Mode.Perm()
	if !a.opts.Trust.trustedUID(info.UID) {
		issues = append(issues, Issue{
			Code:   IssueUntrustedOwner,
			Path:   name,
			Source: source,
			Detail: fmt.Sprintf("owner UID %d is not trusted", info.UID),
		})
	}
	if perm&0o002 != 0 {
		issues = append(issues, Issue{
			Code:   IssueWorldWritable,
			Path:   name,
			Source: source,
			Detail: fmt.Sprintf("mode %04o is world-writable", perm),
		})
	}
	if perm&0o020 != 0 && !a.opts.Trust.trustedGID(info.GID) {
		issues = append(issues, Issue{
			Code:   IssueGroupWritable,
			Path:   name,
			Source: source,
			Detail: fmt.Sprintf("mode %04o is writable by untrusted group %d", perm, info.GID),
		})
	}
	return issues
}

func (a *analyzer) resolve(name string, kind EntryKind, meta ELFMetadata) Dependency {
	expanded, tokenIssues := a.expandTokens(name)
	dependency := Dependency{Name: name, Kind: kind}
	dependency.Issues = append(dependency.Issues, tokenIssues...)

	if name == "" {
		dependency.Issues = append(dependency.Issues, Issue{
			Code:   IssueInvalidPath,
			Path:   name,
			Detail: "empty dependency or preload entry",
		})
		dependency.Issues = dedupeIssues(dependency.Issues)
		return dependency
	}

	var candidates []Candidate
	if strings.Contains(expanded, "/") {
		candidate := a.candidateFromPath(expanded, SourceDirect, tokenIssues)
		candidate.Entry = name
		candidates = append(candidates, candidate)
	} else {
		for _, dir := range a.pathSearchDirs(meta) {
			candidates = append(candidates, a.candidateFromDir(dir, expanded))
		}
		for _, entry := range a.opts.Cache {
			if entry.Name != expanded {
				continue
			}
			candidates = append(candidates, a.candidateFromPath(entry.Path, SourceCache, tokenIssues))
		}
		if !meta.NoDefaultLibs {
			for _, dir := range a.opts.DefaultDirs {
				if dir == "" {
					continue
				}
				absolute, isAbsolute := absoluteForAnalysis(dir, a.cwd)
				issues := append([]Issue(nil), tokenIssues...)
				if !isAbsolute {
					issues = append(issues, Issue{
						Code:   IssueRelativePath,
						Path:   dir,
						Source: SourceDefault,
						Detail: "relative default directory cannot be trusted in secure-execution mode",
					})
				}
				candidates = append(candidates, a.candidateFromDir(searchDir{
					raw:    dir,
					path:   absolute,
					source: SourceDefault,
					issues: issues,
				}, expanded))
			}
		}
	}

	if len(tokenIssues) != 0 {
		for i := range candidates {
			candidates[i].Issues = dedupeIssues(append(candidates[i].Issues, tokenIssues...))
			candidates[i].Safe = false
		}
	}

	for _, candidate := range candidates {
		dependency.Search = append(dependency.Search, candidate)
		dependency.Issues = append(dependency.Issues, candidate.Issues...)
		if candidate.Exists {
			dependency.Resolved = true
			dependency.Path = candidate.Path
			dependency.Directory = candidate.Directory
			dependency.Source = candidate.Source
			break
		}
	}
	if !dependency.Resolved {
		dependency.Issues = append(dependency.Issues, Issue{
			Code:   IssueMissingDependency,
			Path:   name,
			Detail: "no loader search location contains this entry",
		})
	}
	dependency.Issues = dedupeIssues(dependency.Issues)
	dependency.Safe = dependency.Resolved && len(dependency.Issues) == 0
	return dependency
}
