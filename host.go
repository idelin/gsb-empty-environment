package netsec

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// PackageStatus is the final transaction state of a package.
type PackageStatus string

const (
	StatusSucceeded PackageStatus = "succeeded"
	StatusCached    PackageStatus = "cached"
	StatusFailed    PackageStatus = "failed"
	StatusRevoked   PackageStatus = "revoked"
	StatusConflict  PackageStatus = "conflict"
)

// PackageResult reports one package's final visibility.
type PackageResult struct {
	Digest      Digest        `json:"digest"`
	Status      PackageStatus `json:"status"`
	Cached      bool          `json:"cached"`
	OutputFiles []string      `json:"outputFiles,omitempty"`
	Cause       Digest        `json:"cause,omitempty"`
}

// PublishedFile identifies the package and callback action that supplied a
// path in the final install view.
type PublishedFile struct {
	Package Digest   `json:"package"`
	Data    []byte   `json:"data"`
	Script  string   `json:"script,omitempty"`
	Process []string `json:"process,omitempty"`
	Action  Action   `json:"action"`
}

// Result is the outcome of one execute-and-publish transaction. Runtime
// failures are reported in Violations; an error return is reserved for an
// invalid plan or host configuration.
type Result struct {
	Plan         *Plan                    `json:"plan"`
	Packages     map[Digest]PackageResult `json:"packages"`
	Events       []Event                  `json:"events"`
	Violations   []Violation              `json:"violations"`
	Revocations  []Revocation             `json:"revocations"`
	Published    map[string]PublishedFile `json:"published"`
	CacheDigests []Digest                 `json:"cacheDigests"`
}

// Package returns a package result.
func (r *Result) Package(digest Digest) (PackageResult, bool) {
	if r == nil {
		return PackageResult{}, false
	}
	result, ok := r.Packages[digest]
	return result, ok
}

// Visible reports whether a package's outputs may appear in the install view.
func (r *Result) Visible(digest Digest) bool {
	result, ok := r.Package(digest)
	return ok && (result.Status == StatusSucceeded || result.Status == StatusCached)
}

// ViolationsFor returns package-local findings in event order.
func (r *Result) ViolationsFor(digest Digest) []Violation {
	if r == nil {
		return nil
	}
	var out []Violation
	for _, violation := range r.Violations {
		if violation.Package == digest {
			out = append(out, violation)
		}
	}
	return out
}

// HostConfig wires a host policy, registry, virtual roots, cache, and optional
// parallel scheduling limit.
type HostConfig struct {
	Policy      *Policy
	Catalog     *Catalog
	Files       *VirtualFS
	Cache       *Cache
	MaxParallel int
}

// Host is an importable execution-and-publication host. Run serializes
// transactions but executes dependency-ready packages concurrently.
type Host struct {
	policy      *Policy
	catalog     *Catalog
	files       *VirtualFS
	cache       *Cache
	maxParallel int
	runMu       sync.Mutex
}

// NewHost constructs a host. Nil policy is deny-all, nil catalog is empty, nil
// filesystem and cache are created automatically.
func NewHost(config HostConfig) (*Host, error) {
	if config.MaxParallel < 0 {
		return nil, fmt.Errorf("netsec: MaxParallel must not be negative")
	}
	policy := config.Policy
	if policy == nil {
		policy = NewPolicy()
	}
	catalog := config.Catalog
	if catalog == nil {
		catalog = NewCatalog()
	}
	files := config.Files
	if files == nil {
		files = NewVirtualFS()
	}
	cache := config.Cache
	if cache == nil {
		cache = NewCache()
	}
	return &Host{
		policy:      policy,
		catalog:     catalog,
		files:       files,
		cache:       cache,
		maxParallel: config.MaxParallel,
	}, nil
}

// Files returns the host's virtual roots.
func (h *Host) Files() *VirtualFS { return h.files }

// Cache returns the host's immutable successful-output cache.
func (h *Host) Cache() *Cache { return h.cache }

// Plan emits per-package capability manifests.
func (h *Host) Plan(graph PackageGraph) (*Plan, error) {
	if h == nil {
		return nil, fmt.Errorf("netsec: nil host")
	}
	return NewPlanner(h.policy, h.catalog).Plan(graph)
}

// Install plans and runs one graph.
func (h *Host) Install(ctx context.Context, graph PackageGraph, runners Runners) (*Result, error) {
	plan, err := h.Plan(graph)
	if err != nil {
		return nil, err
	}
	return h.Run(ctx, plan, runners)
}

type packageOutcome struct {
	status     PackageStatus
	cached     bool
	files      map[string][]byte
	provenance map[string]WriteProvenance
	events     []Event
	violations []Violation
}

// Run executes synthetic callbacks and publishes outputs in deterministic
// dependency order. The plan must have been produced by a Planner; modified or
// untrusted plans are rejected. It returns a Result for security and callback
// failures.
func (h *Host) Run(ctx context.Context, plan *Plan, runners Runners) (*Result, error) {
	if h == nil {
		return nil, fmt.Errorf("netsec: nil host")
	}
	if err := plan.validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	h.runMu.Lock()
	defer h.runMu.Unlock()

	for _, digest := range plan.Order {
		if err := h.files.ensureSourceRoot(digest); err != nil {
			return nil, err
		}
		h.files.resetOutput(digest)
	}
	h.files.seal()
	defer h.files.unseal()

	catalog := h.catalog.snapshot()
	outcomes := make(map[Digest]*packageOutcome, len(plan.Order))
	statuses := make(map[Digest]PackageStatus, len(plan.Order))
	pending := make(map[Digest]bool, len(plan.Order))
	for _, digest := range plan.Order {
		pending[digest] = true
	}

	for len(pending) > 0 {
		var ready []Digest
		for _, digest := range plan.Order {
			if !pending[digest] {
				continue
			}
			manifest := plan.Manifests[digest]
			dependenciesReady := true
			for _, dep := range manifest.Dependencies {
				status, done := statuses[dep]
				if !done || (status != StatusSucceeded && status != StatusCached) {
					dependenciesReady = false
					break
				}
			}
			if dependenciesReady {
				ready = append(ready, digest)
			}
		}
		if len(ready) == 0 {
			break
		}
		batch := h.runBatch(ctx, plan, runners, catalog, ready)
		for _, digest := range ready {
			pending[digest] = false
			outcome := batch[digest]
			outcomes[digest] = outcome
			statuses[digest] = outcome.status
		}
	}

	revocations := make(map[Digest]Revocation)
	h.revokeBlockedPackages(plan, statuses, outcomes, revocations)
	h.publish(plan, statuses, outcomes, revocations)

	result := &Result{
		Plan:         plan,
		Packages:     make(map[Digest]PackageResult, len(plan.Order)),
		Published:    make(map[string]PublishedFile),
		CacheDigests: h.cache.Digests(),
	}
	for _, digest := range plan.Order {
		status := statuses[digest]
		if status == "" {
			status = StatusRevoked
			statuses[digest] = status
		}
		outcome := outcomes[digest]
		packageResult := PackageResult{Digest: digest, Status: status}
		if outcome != nil {
			packageResult.Cached = outcome.cached
			if status == StatusSucceeded || status == StatusCached {
				packageResult.OutputFiles = sortedKeys(outcome.files)
			}
			result.Events = append(result.Events, outcome.events...)
			result.Violations = append(result.Violations, outcome.violations...)
		}
		if revocation, ok := revocations[digest]; ok {
			packageResult.Cause = revocation.Cause
		}
		result.Packages[digest] = packageResult
	}
	result.Revocations = sortedRevocations(plan, revocations)

	for _, digest := range plan.Order {
		status := statuses[digest]
		if status != StatusSucceeded && status != StatusCached {
			continue
		}
		outcome := outcomes[digest]
		if outcome == nil {
			continue
		}
		paths := sortedKeys(outcome.files)
		for _, path := range paths {
			provenance := provenanceFor(path, outcome)
			result.Published[path] = PublishedFile{
				Package: digest,
				Data:    append([]byte(nil), outcome.files[path]...),
				Script:  provenance.Script,
				Process: append([]string(nil), provenance.Process...),
				Action:  provenance.Action,
			}
		}
	}
	return result, nil
}

func (h *Host) runBatch(ctx context.Context, plan *Plan, runners Runners, catalog catalogSnapshot, ready []Digest) map[Digest]*packageOutcome {
	outcomes := make(map[Digest]*packageOutcome, len(ready))
	var mu sync.Mutex
	var wg sync.WaitGroup
	limit := h.maxParallel
	if limit <= 0 || limit > len(ready) {
		limit = len(ready)
	}
	semaphore := make(chan struct{}, limit)
	for _, digest := range ready {
		digest := digest
		wg.Add(1)
		go func() {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			outcome := h.runPackage(ctx, plan, runners, catalog, digest)
			mu.Lock()
			outcomes[digest] = outcome
			mu.Unlock()
		}()
	}
	wg.Wait()
	return outcomes
}

func (h *Host) runPackage(ctx context.Context, plan *Plan, runners Runners, catalog catalogSnapshot, digest Digest) *packageOutcome {
	h.files.resetOutput(digest)
	if entry, ok := h.cache.Get(digest); ok {
		return &packageOutcome{
			status:     StatusCached,
			cached:     true,
			files:      entry.Files,
			provenance: entry.Provenance,
		}
	}
	if err := ctx.Err(); err != nil {
		outcome := failedOutcome(CodeCallbackFailure, digest, "", "callback context was canceled")
		h.cache.Remove(digest)
		return outcome
	}

	manifest, ok := plan.Manifests[digest]
	if !ok {
		return failedOutcome(CodeCallbackFailure, digest, "", "package manifest is missing")
	}
	state := newPackageState()
	for _, script := range manifest.Scripts {
		runner, exists := runners[ScriptRef{Package: digest, Name: script.Name}]
		if !exists || runner == nil {
			state.callbackFailure(Violation{
				Code:    CodeCallbackFailure,
				Package: digest,
				Script:  script.Name,
				Process: []string{script.Name},
				Message: "script callback is missing",
			})
			break
		}
		execution := &Execution{
			roots:     h.files,
			state:     state,
			catalog:   catalog,
			ctx:       ctx,
			pkg:       digest,
			script:    script.Name,
			process:   []string{script.Name},
			declared:  NewActionSet(script.Declared...),
			effective: effectiveActions(h.policy, catalog, digest, script.Granted),
		}
		err, panicked := invokeRunner(ctx, runner, execution)
		switch {
		case panicked:
			state.callbackFailure(Violation{
				Code:    CodeCallbackFailure,
				Package: digest,
				Script:  script.Name,
				Process: []string{script.Name},
				Message: "script callback panicked",
			})
		case err != nil:
			var violation *ViolationError
			if !errors.As(err, &violation) {
				state.callbackFailure(Violation{
					Code:    CodeCallbackFailure,
					Package: digest,
					Script:  script.Name,
					Process: []string{script.Name},
					Message: "script callback returned an error",
				})
			}
		case ctx.Err() != nil:
			state.callbackFailure(Violation{
				Code:    CodeCallbackFailure,
				Package: digest,
				Script:  script.Name,
				Process: []string{script.Name},
				Message: "callback context was canceled",
			})
		}
		if state.hasFailed() {
			break
		}
	}
	events, violations, provenance, failed := state.snapshot()
	if failed {
		h.files.resetOutput(digest)
		h.cache.Remove(digest)
		return &packageOutcome{
			status:     StatusFailed,
			events:     events,
			violations: violations,
		}
	}
	files := h.files.OutputFiles(digest)
	if err := h.cache.Put(digest, files, provenance); err != nil {
		h.files.resetOutput(digest)
		return &packageOutcome{
			status: StatusFailed,
			events: events,
			violations: []Violation{{
				Code:    CodeCallbackFailure,
				Package: digest,
				Process: []string{},
				Message: "could not snapshot package output",
			}},
		}
	}
	return &packageOutcome{
		status:     StatusSucceeded,
		files:      files,
		provenance: provenance,
		events:     events,
		violations: violations,
	}
}

func effectiveActions(policy *Policy, catalog catalogSnapshot, digest Digest, planned []Action) ActionSet {
	effective := NewActionSet(planned...).Intersect(policy.Granted(digest))
	filtered := NewActionSet()
	for _, action := range effective.List() {
		if catalogActionAllowed(catalog, action) {
			filtered = filtered.Union(NewActionSet(action))
		}
	}
	return filtered
}

func failedOutcome(code Code, digest Digest, script, message string) *packageOutcome {
	return &packageOutcome{
		status: StatusFailed,
		violations: []Violation{{
			Code:    code,
			Package: digest,
			Script:  script,
			Message: message,
		}},
	}
}

func (h *Host) revokeBlockedPackages(plan *Plan, statuses map[Digest]PackageStatus, outcomes map[Digest]*packageOutcome, revocations map[Digest]Revocation) {
	failed := make(map[Digest]bool)
	for digest, status := range statuses {
		if status == StatusFailed {
			failed[digest] = true
		}
	}
	if len(failed) == 0 {
		return
	}
	dependents := buildDependents(plan)
	for _, target := range plan.Order {
		if statuses[target] != "" {
			continue
		}
		var cause Digest
		var chain []Digest
		for _, candidate := range plan.Order {
			if !failed[candidate] {
				continue
			}
			if path := dependencyPath(candidate, target, dependents); path != nil {
				cause = candidate
				chain = path
				break
			}
		}
		statuses[target] = StatusRevoked
		h.cache.Remove(target)
		h.files.resetOutput(target)
		if outcome := outcomes[target]; outcome != nil {
			outcome.files = nil
			outcome.provenance = nil
		}
		if cause != "" {
			revocations[target] = Revocation{
				Code:    CodeDependencyRevoked,
				Package: target,
				Cause:   cause,
				Chain:   chain,
			}
		}
	}
}

func (h *Host) publish(plan *Plan, statuses map[Digest]PackageStatus, outcomes map[Digest]*packageOutcome, revocations map[Digest]Revocation) {
	publishedOwners := make(map[string]Digest)
	dependents := buildDependents(plan)
	for _, digest := range plan.Order {
		status := statuses[digest]
		if status != StatusSucceeded && status != StatusCached {
			continue
		}
		outcome := outcomes[digest]
		if outcome == nil {
			continue
		}
		paths := sortedKeys(outcome.files)
		var conflictPath string
		var conflictOwner Digest
		var conflictOwnerPath string
		for _, path := range paths {
			if owner, ownerPath, exists := publishedOwner(path, publishedOwners); exists {
				conflictPath = path
				conflictOwner = owner
				conflictOwnerPath = ownerPath
				break
			}
		}
		if conflictPath != "" {
			provenance := provenanceFor(conflictPath, outcome)
			violation := Violation{
				Code:    CodePublishConflict,
				Package: digest,
				Script:  provenance.Script,
				Process: append([]string(nil), provenance.Process...),
				Action:  provenance.Action,
				Message: fmt.Sprintf("output path %q conflicts with %q published by package %s", conflictPath, conflictOwnerPath, conflictOwner),
			}
			outcome.violations = append(outcome.violations, violation)
			statuses[digest] = StatusConflict
			outcome.files = nil
			outcome.provenance = nil
			outcome.cached = false
			h.cache.Remove(digest)
			h.files.resetOutput(digest)
			h.revokeDependents(plan, digest, dependents, statuses, outcomes, revocations)
			continue
		}
		for _, path := range paths {
			publishedOwners[path] = digest
		}
	}
}

func (h *Host) revokeDependents(plan *Plan, cause Digest, dependents map[Digest][]Digest, statuses map[Digest]PackageStatus, outcomes map[Digest]*packageOutcome, revocations map[Digest]Revocation) {
	for _, target := range plan.Order {
		if target == cause {
			continue
		}
		path := dependencyPath(cause, target, dependents)
		if path == nil {
			continue
		}
		h.cache.Remove(target)
		h.files.resetOutput(target)
		if outcome := outcomes[target]; outcome != nil {
			outcome.files = nil
			outcome.provenance = nil
			outcome.cached = false
		}
		switch statuses[target] {
		case StatusSucceeded, StatusCached, "":
			statuses[target] = StatusRevoked
		}
		if _, exists := revocations[target]; !exists {
			revocations[target] = Revocation{
				Code:    CodeDependencyRevoked,
				Package: target,
				Cause:   cause,
				Chain:   path,
			}
		}
	}
}

func publishedOwner(path string, owners map[string]Digest) (Digest, string, bool) {
	if owner, exists := owners[path]; exists {
		return owner, path, true
	}
	for i := 1; i < len(path); i++ {
		if path[i] != '/' {
			continue
		}
		if owner, exists := owners[path[:i]]; exists {
			return owner, path[:i], true
		}
	}
	prefix := path + "/"
	for existing, owner := range owners {
		if strings.HasPrefix(existing, prefix) {
			return owner, existing, true
		}
	}
	return "", "", false
}

func buildDependents(plan *Plan) map[Digest][]Digest {
	dependents := make(map[Digest][]Digest, len(plan.Manifests))
	for digest, manifest := range plan.Manifests {
		for _, dep := range manifest.Dependencies {
			dependents[dep] = append(dependents[dep], digest)
		}
	}
	for dep := range dependents {
		sort.Slice(dependents[dep], func(i, j int) bool { return dependents[dep][i] < dependents[dep][j] })
	}
	return dependents
}

func dependencyPath(from, to Digest, dependents map[Digest][]Digest) []Digest {
	if from == to {
		return []Digest{from}
	}
	type item struct {
		digest Digest
		path   []Digest
	}
	queue := []item{{digest: from, path: []Digest{from}}}
	seen := map[Digest]bool{from: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range dependents[current.digest] {
			if seen[next] {
				continue
			}
			nextPath := append(append([]Digest(nil), current.path...), next)
			if next == to {
				return nextPath
			}
			seen[next] = true
			queue = append(queue, item{digest: next, path: nextPath})
		}
	}
	return nil
}

func provenanceFor(path string, outcome *packageOutcome) WriteProvenance {
	if outcome != nil {
		if provenance, ok := outcome.provenance[path]; ok {
			provenance.Process = append([]string(nil), provenance.Process...)
			return provenance
		}
	}
	return WriteProvenance{Action: Write(path)}
}

func sortedKeys(files map[string][]byte) []string {
	keys := make([]string, 0, len(files))
	for path := range files {
		keys = append(keys, path)
	}
	sort.Strings(keys)
	return keys
}

func sortedRevocations(plan *Plan, revocations map[Digest]Revocation) []Revocation {
	out := make([]Revocation, 0, len(revocations))
	for _, digest := range plan.Order {
		if revocation, ok := revocations[digest]; ok {
			revocation.Chain = append([]Digest(nil), revocation.Chain...)
			out = append(out, revocation)
		}
	}
	return out
}
