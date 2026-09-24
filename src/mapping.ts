/**
 * Structural mappings between two versions.
 *
 * A mapping is a pair of index relations aligned with the two versions'
 * character arrays:
 *  - `forward[i]`  = index of from-char i in the to-version, or -1 (deleted)
 *  - `backward[j]` = index of to-char j in the from-version, or -1 (inserted)
 *
 * Mappings compose (`composeMappings`) and invert (`invertMapping`)
 * structurally; offsets are always derived from these relations, never
 * stored as the primary representation.
 */

import type { VersionId } from './model.ts';

export interface Mapping {
  from: VersionId;
  to: VersionId;
  forward: number[];
  backward: number[];
}

export type Stickiness = 'left' | 'right';

export function identityMapping(version: VersionId, length: number): Mapping {
  const ids = Array.from({ length }, (_, i) => i);
  return { from: version, to: version, forward: ids.slice(), backward: ids };
}

export function composeMappings(first: Mapping, second: Mapping): Mapping {
  if (first.to !== second.from) {
    throw new Error(`cannot compose ${first.from}->${first.to} with ${second.from}->${second.to}`);
  }
  const forward = first.forward.map((t) => (t < 0 ? -1 : second.forward[t]));
  const backward = second.backward.map((s) => (s < 0 ? -1 : first.backward[s]));
  return { from: first.from, to: second.to, forward, backward };
}

export function invertMapping(m: Mapping): Mapping {
  return { from: m.to, to: m.from, forward: m.backward, backward: m.forward };
}

/**
 * Map a boundary (a gap between characters, 0..n) through a mapping.
 * Stickiness decides which neighbouring character the position clings to;
 * if that character was deleted, the position falls back to the nearest
 * surviving character on the other side, collapsing onto the deletion site.
 */
export function mapBoundary(m: Mapping, boundary: number, stick: Stickiness): number {
  const n = m.forward.length;
  const b = Math.max(0, Math.min(boundary, n));
  if (stick === 'right') {
    for (let j = b; j < n; j++) {
      const t = m.forward[j];
      if (t >= 0) return t;
    }
    for (let j = b - 1; j >= 0; j--) {
      const t = m.forward[j];
      if (t >= 0) return t + 1;
    }
    return 0;
  }
  for (let j = b - 1; j >= 0; j--) {
    const t = m.forward[j];
    if (t >= 0) return t + 1;
  }
  for (let j = b; j < n; j++) {
    const t = m.forward[j];
    if (t >= 0) return t;
  }
  return 0;
}

/**
 * True when a boundary in the *target* of mapping `m` sits next to
 * characters that have no image in the target (i.e. deleted, when `m` is an
 * inverse mapping used to travel back in time). Such positions carry lost
 * information: the exact source location cannot be recovered uniquely.
 */
export function hasLostContext(m: Mapping, boundaryInTarget: number): boolean {
  const arr = m.backward; // per char of m.to; -1 means "no counterpart in m.from"
  const left = boundaryInTarget > 0 && arr[boundaryInTarget - 1] === -1;
  const right = boundaryInTarget < arr.length && arr[boundaryInTarget] === -1;
  return left || right;
}
