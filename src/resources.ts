/**
 * Resource authorizations are prefix differences:
 *
 *   { prefix: "a", excludes: ["a/private"] }
 *
 * denotes every resource beginning with `a`, except resources beginning with
 * `a/private`.  Prefix matching is literal string-prefix matching.  A later
 * sublease is always intersected with its parent; it can never widen a scope.
 */

export interface ResourceInterval {
  prefix: string;
  excludes?: string[];
}

export type ResourceScope = ResourceInterval[];
export type ResourceScopeInput = ResourceScope | ResourceInterval;

export function canonicalResourceInterval(input: ResourceInterval): ResourceInterval | null {
  const prefix = input.prefix;
  const raw = [...new Set(input.excludes ?? [])];
  const excludes: string[] = [];
  for (const candidate of raw) {
    if (!candidate.startsWith(prefix)) continue;
    if (candidate === prefix) return null;
    if (excludes.some((existing) => candidate.startsWith(existing))) continue;
    for (let i = excludes.length - 1; i >= 0; i--) {
      if (excludes[i].startsWith(candidate)) excludes.splice(i, 1);
    }
    excludes.push(candidate);
  }
  excludes.sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
  return excludes.length === 0 ? { prefix } : { prefix, excludes };
}

export function normalizeResourceScope(scope: ResourceScopeInput): ResourceScope {
  const output: ResourceScope = [];
  const input = Array.isArray(scope) ? scope : [scope];
  for (const raw of input) {
    const interval = canonicalResourceInterval(raw);
    if (interval === null) continue;
    let pieces: ResourceScope = [interval];
    for (const existing of output) {
      pieces = pieces.flatMap((piece) => subtractResourceInterval(piece, existing));
      if (pieces.length === 0) break;
    }
    output.push(...pieces);
  }
  return output;
}

export function resourceIntervalIsEmpty(interval: ResourceInterval): boolean {
  return canonicalResourceInterval(interval) === null;
}

export function scopeIsEmpty(scope: ResourceScopeInput): boolean {
  return normalizeResourceScope(scope).length === 0;
}

export function intersectResourceIntervals(
  a: ResourceInterval,
  b: ResourceInterval,
): ResourceInterval | null {
  const ca = canonicalResourceInterval(a);
  const cb = canonicalResourceInterval(b);
  if (ca === null || cb === null) return null;
  if (!ca.prefix.startsWith(cb.prefix) && !cb.prefix.startsWith(ca.prefix)) return null;
  const prefix = ca.prefix.length >= cb.prefix.length ? ca.prefix : cb.prefix;
  return canonicalResourceInterval({
    prefix,
    excludes: [...(ca.excludes ?? []), ...(cb.excludes ?? [])],
  });
}

export function intersectResourceScopes(
  a: ResourceScopeInput,
  b: ResourceScopeInput,
): ResourceScope {
  const output: ResourceScope = [];
  for (const left of normalizeResourceScope(a)) {
    for (const right of normalizeResourceScope(b)) {
      const hit = intersectResourceIntervals(left, right);
      if (hit !== null) output.push(hit);
    }
  }
  return normalizeResourceScope(output);
}

/**
 * A \ B.  Every prefix of B is excluded; the result is a finite union of
 * prefix-difference intervals.  The first component lies outside B's base and
 * the remaining components lie inside one of B's excluded prefixes.
 */
export function subtractResourceInterval(a: ResourceInterval, b: ResourceInterval): ResourceScope {
  const ca = canonicalResourceInterval(a);
  const cb = canonicalResourceInterval(b);
  if (ca === null) return [];
  if (cb === null) return [ca];

  const pieces: ResourceScope = [];
  if (!ca.prefix.startsWith(cb.prefix) && !cb.prefix.startsWith(ca.prefix)) {
    pieces.push(ca);
  } else if (cb.prefix.startsWith(ca.prefix) && cb.prefix !== ca.prefix) {
    const outside = canonicalResourceInterval({
      prefix: ca.prefix,
      excludes: [...(ca.excludes ?? []), cb.prefix],
    });
    if (outside !== null) pieces.push(outside);
  } else if (ca.prefix.startsWith(cb.prefix) && ca.prefix !== cb.prefix) {
    // A is entirely inside B's base, so only B's excluded holes survive.
  } else {
    // Equal bases: the whole base is inside B's base.
  }

  for (const excluded of cb.excludes ?? []) {
    const hit = intersectResourceIntervals(ca, { prefix: excluded });
    if (hit !== null) pieces.push(hit);
  }

  return normalizeResourceScope(pieces);
}

export function subtractResourceScopes(
  a: ResourceScopeInput,
  b: ResourceScopeInput,
): ResourceScope {
  let current = normalizeResourceScope(a);
  for (const cut of normalizeResourceScope(b)) {
    current = current.flatMap((piece) => subtractResourceInterval(piece, cut));
    if (current.length === 0) break;
  }
  return current;
}

export function resourceIntervalContains(interval: ResourceInterval, resource: string): boolean {
  const canonical = canonicalResourceInterval(interval);
  if (canonical === null || !resource.startsWith(canonical.prefix)) return false;
  return !(canonical.excludes ?? []).some((excluded) => resource.startsWith(excluded));
}

export function scopeContains(scope: ResourceScopeInput, resource: string): boolean {
  return normalizeResourceScope(scope).some((interval) =>
    resourceIntervalContains(interval, resource),
  );
}

export function resourceScopeEquals(a: ResourceScopeInput, b: ResourceScopeInput): boolean {
  const left = normalizeResourceScope(a);
  const right = normalizeResourceScope(b);
  if (left.length !== right.length) return false;
  return left.every((x, i) => resourceIntervalEquals(x, right[i]));
}

export function resourceIntervalEquals(a: ResourceInterval, b: ResourceInterval): boolean {
  const ca = canonicalResourceInterval(a);
  const cb = canonicalResourceInterval(b);
  if (ca === null || cb === null) return ca === cb;
  if (ca.prefix !== cb.prefix) return false;
  const ea = ca.excludes ?? [];
  const eb = cb.excludes ?? [];
  return ea.length === eb.length && ea.every((value, i) => value === eb[i]);
}

export function scopeIsSubset(a: ResourceScopeInput, b: ResourceScopeInput): boolean {
  return subtractResourceScopes(a, b).length === 0;
}

export function resourceScopeToJSON(scope: ResourceScopeInput): ResourceInterval[] {
  return normalizeResourceScope(scope).map((interval) => ({
    prefix: interval.prefix,
    ...(interval.excludes?.length ? { excludes: [...interval.excludes] } : {}),
  }));
}

/**
 * Deduplicate cuts without deleting a nested cut.  Partitioning must retain
 * both `a` and `a/x`; normalizing them as a union would discard `a/x` and
 * collapse a cell boundary that matters for returns and subleases.
 */
export function resourceCutList(scope: ResourceScopeInput): ResourceScope {
  const output: ResourceScope = [];
  const seen = new Set<string>();
  const input = Array.isArray(scope) ? scope : [scope];
  for (const raw of input) {
    const interval = canonicalResourceInterval(raw);
    if (interval === null) continue;
    const key = `${interval.prefix}\u0000${(interval.excludes ?? []).join("\u0000")}`;
    if (seen.has(key)) continue;
    seen.add(key);
    output.push(interval);
  }
  return output;
}

/** Split a scope into cells, each wholly inside or wholly outside every cut. */
export function partitionScopeByCuts(
  scope: ResourceScopeInput,
  cuts: ResourceScopeInput,
): ResourceScope {
  let cells = normalizeResourceScope(scope);
  for (const cut of resourceCutList(cuts)) {
    const next: ResourceScope = [];
    for (const cell of cells) {
      const inside = intersectResourceIntervals(cell, cut);
      if (inside !== null) next.push(inside);
      next.push(...subtractResourceInterval(cell, cut));
    }
    cells = normalizeResourceScope(next);
  }
  return cells;
}

export function describeResourceInterval(interval: ResourceInterval): string {
  const canonical = canonicalResourceInterval(interval);
  if (canonical === null) return "<empty>";
  const excludes = canonical.excludes ?? [];
  if (excludes.length === 0) return `prefix(${JSON.stringify(canonical.prefix)})`;
  return `prefix(${JSON.stringify(canonical.prefix)}) \\ { ${excludes
    .map((value) => JSON.stringify(value))
    .join(", ")} }`;
}
