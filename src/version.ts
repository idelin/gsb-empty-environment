import type { AssemblyIdentity, AssemblyRef, VersionRange } from './types.ts';

/** Parse a four-part (or shorter) .NET-style version into numeric parts. */
export function parseVersion(v: string): number[] {
  if (!/^\d+(\.\d+){0,3}$/.test(v)) {
    throw new Error(`illegal version: ${v}`);
  }
  const parts = v.split('.').map(Number);
  while (parts.length < 4) parts.push(0);
  return parts;
}

export function compareVersions(a: string, b: string): number {
  const pa = parseVersion(a);
  const pb = parseVersion(b);
  for (let i = 0; i < 4; i++) {
    if (pa[i] !== pb[i]) return pa[i] < pb[i] ? -1 : 1;
  }
  return 0;
}

export function versionInRange(version: string, range: VersionRange): boolean {
  if (range.min !== undefined && compareVersions(version, range.min) < 0) {
    return false;
  }
  if (range.max !== undefined && compareVersions(version, range.max) > 0) {
    return false;
  }
  return true;
}

/** True when every point covered by `inner` is also covered by `outer`. */
export function rangeSubset(inner: VersionRange, outer: VersionRange): boolean {
  const innerMin = inner.min ?? '0.0.0.0';
  const outerMin = outer.min ?? '0.0.0.0';
  if (compareVersions(innerMin, outerMin) < 0) return false;
  if (inner.max === undefined) {
    if (outer.max !== undefined) return false;
  } else if (outer.max === undefined) {
    // inner bounded, outer open: contained.
  } else if (compareVersions(inner.max, outer.max) > 0) {
    return false;
  }
  return true;
}

/** Every version accepted by `inner` is accepted by some range of `outer`. */
export function rangeListSubset(inner: VersionRange[], outer: VersionRange[]): boolean {
  return inner.every((r) => outer.some((o) => rangeSubset(r, o)));
}

export function assemblyMatches(ref: AssemblyRef, id: AssemblyIdentity): boolean {
  if (ref.publicKey !== id.publicKey) return false;
  if (ref.name !== undefined && ref.name !== id.name) return false;
  return ref.versions.some((r) => versionInRange(id.version, r));
}

/**
 * Head containment: every concrete assembly the new (`inner`) ref can denote
 * must be denotable by the old (`outer`) ref. Same-name/different-key
 * assemblies never match.
 */
export function assemblyRefSubset(inner: AssemblyRef, outer: AssemblyRef): boolean {
  if (inner.publicKey !== outer.publicKey) return false;
  if (inner.name !== outer.name) return false;
  return rangeListSubset(inner.versions, outer.versions);
}
