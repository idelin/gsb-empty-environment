// Package relocation guards Maven-style dependency relocations.
//
// An attacker can publish a POM whose relocation points a widely used
// coordinate at a groupId they control. This package enforces that:
//   - at most MaxHops relocations are followed and relocation chains never
//     contain a cycle,
//   - every target groupId shares the original groupId's registered namespace
//     owner or has been explicitly approved,
//   - groupIds are validated (no empty segments, ".." or backslashes) before
//     being converted into repository paths,
//   - a rejected relocation never downloads the target artifact.
package relocation

import (
	"context"
	"errors"
)

// DefaultMaxHops is the maximum number of relocations followed for one
// resolution.
const DefaultMaxHops = 3

var (
	// ErrMaxHops is returned when a relocation chain exceeds MaxHops.
	ErrMaxHops = errors.New("relocation: maximum relocation hops exceeded")
	// ErrCycle is returned when a relocation chain revisits a coordinate.
	ErrCycle = errors.New("relocation: relocation cycle detected")
	// ErrCrossOwner is returned when a target groupId belongs to another
	// namespace owner and has not been explicitly approved.
	ErrCrossOwner = errors.New("relocation: target groupId is owned by another namespace owner")
)

// Coordinate identifies a Maven artifact.
type Coordinate struct {
	GroupID    string
	ArtifactID string
	Version    string
}

// RelocationSource resolves the relocation POM of a coordinate, if any.
// A nil target with a nil error means the coordinate has no relocation.
type RelocationSource interface {
	Relocation(ctx context.Context, coord Coordinate) (target *Coordinate, err error)
}

// ArtifactDownloader fetches the artifact behind a fully resolved coordinate.
type ArtifactDownloader interface {
	Download(ctx context.Context, coord Coordinate) error
}

// Guard resolves coordinates through a RelocationSource while enforcing the
// package's relocation policy.
type Guard struct {
	// Registry holds the trusted namespace owners and explicit approvals.
	Registry *Registry
	// Source supplies relocation POMs.
	Source RelocationSource
	// MaxHops caps the relocations followed per resolution; zero means
	// DefaultMaxHops.
	MaxHops int
}

func (g *Guard) maxHops() int {
	if g.MaxHops <= 0 {
		return DefaultMaxHops
	}
	return g.MaxHops
}

// Resolve follows the relocation chain starting at coord and returns the final
// coordinate. The original groupId is validated once and every relocation is
// checked against the hop limit, cycle detector, path rules and owner policy
// before the next POM is consulted.
func (g *Guard) Resolve(ctx context.Context, coord Coordinate) (Coordinate, error) {
	if g.Registry == nil {
		return Coordinate{}, errors.New("relocation: guard requires a registry")
	}
	if g.Source == nil {
		return Coordinate{}, errors.New("relocation: guard requires a relocation source")
	}
	if err := CheckGroupID(coord.GroupID); err != nil {
		return Coordinate{}, err
	}

	originalGroupID := coord.GroupID
	current := coord
	visited := map[Coordinate]struct{}{coord: {}}
	hops := 0

	for {
		target, err := g.Source.Relocation(ctx, current)
		if err != nil {
			return Coordinate{}, err
		}
		if target == nil {
			return current, nil
		}

		hops++
		if hops > g.maxHops() {
			return Coordinate{}, ErrMaxHops
		}
		if _, seen := visited[*target]; seen {
			return Coordinate{}, ErrCycle
		}
		if err := CheckGroupID(target.GroupID); err != nil {
			return Coordinate{}, err
		}
		if !g.Registry.Allowed(originalGroupID, target.GroupID) {
			return Coordinate{}, ErrCrossOwner
		}

		visited[*target] = struct{}{}
		current = *target
	}
}

// Download resolves coord and, only when the full relocation chain is accepted,
// downloads the final artifact through downloader. A rejected relocation
// returns without ever invoking the downloader, so no target artifact is
// fetched.
func (g *Guard) Download(ctx context.Context, coord Coordinate, downloader ArtifactDownloader) (Coordinate, error) {
	if downloader == nil {
		return Coordinate{}, errors.New("relocation: download requires an artifact downloader")
	}
	resolved, err := g.Resolve(ctx, coord)
	if err != nil {
		return Coordinate{}, err
	}
	if err := downloader.Download(ctx, resolved); err != nil {
		return Coordinate{}, err
	}
	return resolved, nil
}
