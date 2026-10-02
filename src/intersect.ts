/**
 * Multi-term AND evaluation over posting lists.
 *
 * Iterators are ordered by ascending docFreq so the shortest list drives the
 * loop. Longer lists are positioned with advance(), which uses skip pointers
 * to leap over regions that cannot match. This is the key win when posting
 * list lengths are highly skewed.
 */

import type { PostingsIterator } from "./postings.ts";

export interface IntersectStats {
  /** Postings decoded per input term (same order as the `terms` arg). */
  decodedPerTerm: Record<string, number>;
  /** Skip-pointer jumps per input term. */
  skipsPerTerm: Record<string, number>;
  /** Total postings decoded across all lists. */
  totalDecoded: number;
}

export interface IntersectResult {
  docIds: number[];
  stats: IntersectStats;
}

/**
 * Intersects the posting lists of `terms` (document-at-a-time AND).
 * `getPostings` returns a fresh iterator per term, or null if absent.
 * When `useSkips` is false, advance() degenerates to a linear scan
 * (baseline for benchmarks).
 */
export function intersectAnd(
  terms: string[],
  getPostings: (term: string) => PostingsIterator | null,
  useSkips = true,
): IntersectResult {
  const active: { term: string; it: PostingsIterator }[] = [];
  for (const term of terms) {
    const it = getPostings(term);
    if (!it || it.done()) {
      return emptyResult(terms);
    }
    active.push({ term, it });
  }
  active.sort((a, b) => a.it.docFreq - b.it.docFreq);

  const docIds: number[] = [];
  const driver = active[0].it;
  const rest = active.slice(1).map((a) => a.it);

  outer: while (!driver.done()) {
    const candidate = driver.docId();
    for (const it of rest) {
      if (it.docId() < candidate) {
        if (useSkips) {
          it.advance(candidate);
        } else {
          while (!it.done() && it.docId() < candidate) it.next();
        }
      }
      if (it.done()) break outer;
      if (it.docId() > candidate) {
        // Realign the driver with the furthest-ahead list and retry.
        driver.advance(it.docId());
        continue outer;
      }
    }
    docIds.push(candidate);
    driver.next();
  }

  const stats: IntersectStats = {
    decodedPerTerm: {},
    skipsPerTerm: {},
    totalDecoded: 0,
  };
  for (const { term, it } of active) {
    stats.decodedPerTerm[term] = it.decodedCount;
    stats.skipsPerTerm[term] = it.skipJumps;
    stats.totalDecoded += it.decodedCount;
  }
  return { docIds, stats };
}

function emptyResult(terms: string[]): IntersectResult {
  const decodedPerTerm: Record<string, number> = {};
  const skipsPerTerm: Record<string, number> = {};
  for (const term of terms) {
    decodedPerTerm[term] = 0;
    skipsPerTerm[term] = 0;
  }
  return { docIds: [], stats: { decodedPerTerm, skipsPerTerm, totalDecoded: 0 } };
}
