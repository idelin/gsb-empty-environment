import type { Segment, Variant } from "./pattern.ts";
import { comparePriority, matchVariant, type Priority } from "./match.ts";

export interface CompiledRoute {
  source: string;
  index: number;
  variants: Variant[];
}

export interface Conflict {
  routes: [number, number];
  sources: [string, string];
  priority: Priority;
  witness: string;
}

const FRESH = "x";
const MAX_SPECIALIZED_WITNESSES = 256;
const MAX_SPECIALIZATION_LITERALS = 6;

export function bestPriority(route: CompiledRoute, segments: string[]): Priority | null {
  let best: Priority | null = null;
  for (const variant of route.variants) {
    if (matchVariant(variant, segments) === null) continue;
    if (best === null || comparePriority(variant, best) < 0) {
      best = { literalCount: variant.literalCount, omittedCount: variant.omittedCount };
    }
  }
  return best;
}

interface Intersection {
  segments: string[];
  fresh: number[];
}

export function intersect(a: Segment[], b: Segment[]): Intersection | null {
  const segments: string[] = [];
  const fresh: number[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    const sa = a[i]!;
    const sb = b[j]!;
    if (sa.kind === "wild") return absorb(b, j, segments, fresh);
    if (sb.kind === "wild") return absorb(a, i, segments, fresh);
    if (sa.kind === "lit" && sb.kind === "lit") {
      if (sa.text !== sb.text) return null;
      segments.push(sa.text);
    } else if (sa.kind === "lit") {
      segments.push(sa.text);
    } else if (sb.kind === "lit") {
      segments.push(sb.text);
    } else {
      fresh.push(segments.length);
      segments.push(FRESH);
    }
    i++;
    j++;
  }
  if (i < a.length) {
    return a[i]!.kind === "wild" ? { segments, fresh } : null;
  }
  if (j < b.length) {
    return b[j]!.kind === "wild" ? { segments, fresh } : null;
  }
  return { segments, fresh };
}

function absorb(seq: Segment[], from: number, segments: string[], fresh: number[]): Intersection {
  for (let k = from; k < seq.length; k++) {
    const seg = seq[k]!;
    if (seg.kind === "wild") return { segments, fresh };
    if (seg.kind === "lit") {
      segments.push(seg.text);
    } else {
      fresh.push(segments.length);
      segments.push(FRESH);
    }
  }
  return { segments, fresh };
}

function specialize(base: Intersection, literals: string[]): string[][] {
  const results: string[][] = [base.segments];
  if (base.fresh.length === 0 || literals.length === 0) return results;
  const current = base.segments.slice();
  const rec = (idx: number): void => {
    if (results.length >= MAX_SPECIALIZED_WITNESSES) return;
    if (idx === base.fresh.length) {
      results.push(current.slice());
      return;
    }
    for (const literal of literals) {
      current[base.fresh[idx]!] = literal;
      rec(idx + 1);
    }
  };
  rec(0);
  return results;
}

export function encodePath(segments: string[]): string {
  return segments.length === 0 ? "/" : "/" + segments.map(encodeURIComponent).join("/");
}

function routeLiterals(route: CompiledRoute): string[] {
  const literals = new Set<string>();
  for (const variant of route.variants) {
    for (const seg of variant.segments) {
      if (seg.kind === "lit") literals.add(seg.text);
    }
  }
  return [...literals];
}

function pairConflict(a: CompiledRoute, b: CompiledRoute): Conflict | null {
  const literals = [...new Set([...routeLiterals(a), ...routeLiterals(b)])]
    .filter((text) => text !== FRESH)
    .slice(0, MAX_SPECIALIZATION_LITERALS);
  let best: { priority: Priority; witness: string[] } | null = null;
  for (const va of a.variants) {
    for (const vb of b.variants) {
      const mgu = intersect(va.segments, vb.segments);
      if (mgu === null) continue;
      for (const witness of specialize(mgu, literals)) {
        const pa = bestPriority(a, witness);
        const pb = bestPriority(b, witness);
        if (pa === null || pb === null || comparePriority(pa, pb) !== 0) continue;
        if (best === null || witness.length < best.witness.length) {
          best = { priority: pa, witness };
        }
      }
    }
  }
  if (best === null) return null;
  return {
    routes: [a.index, b.index],
    sources: [a.source, b.source],
    priority: best.priority,
    witness: encodePath(best.witness),
  };
}

export function findConflicts(routes: CompiledRoute[]): Conflict[] {
  const conflicts: Conflict[] = [];
  for (let i = 0; i < routes.length; i++) {
    for (let j = i + 1; j < routes.length; j++) {
      const conflict = pairConflict(routes[i]!, routes[j]!);
      if (conflict) conflicts.push(conflict);
    }
  }
  return conflicts;
}

export function enumerateConflicts(routes: CompiledRoute[], maxDepth: number): Conflict[] {
  const literals = new Set<string>();
  for (const route of routes) {
    for (const text of routeLiterals(route)) literals.add(text);
  }
  const alphabet = [...literals, "x", "y"];
  const byPair = new Map<string, Conflict>();
  let level: string[][] = [[]];
  for (let depth = 0; ; depth++) {
    const next: string[][] = [];
    for (const path of level) {
      const hits: { route: CompiledRoute; priority: Priority }[] = [];
      for (const route of routes) {
        const priority = bestPriority(route, path);
        if (priority) hits.push({ route, priority });
      }
      for (let x = 0; x < hits.length; x++) {
        for (let y = x + 1; y < hits.length; y++) {
          const ha = hits[x]!;
          const hb = hits[y]!;
          if (comparePriority(ha.priority, hb.priority) !== 0) continue;
          const lo = Math.min(ha.route.index, hb.route.index);
          const hi = Math.max(ha.route.index, hb.route.index);
          const key = `${lo}:${hi}`;
          if (!byPair.has(key)) {
            byPair.set(key, {
              routes: [lo, hi],
              sources: [
                routes.find((r) => r.index === lo)!.source,
                routes.find((r) => r.index === hi)!.source,
              ],
              priority: ha.priority,
              witness: encodePath(path),
            });
          }
        }
      }
      if (depth < maxDepth) {
        for (const symbol of alphabet) next.push([...path, symbol]);
      }
    }
    if (depth >= maxDepth) break;
    level = next;
  }
  return [...byPair.values()];
}
