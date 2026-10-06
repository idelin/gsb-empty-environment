import { defaultNormalize } from './normalize.ts';
import type { Candidate, NormalizeFn, Suggestion } from './types.ts';

interface Contribution {
  readonly score: number;
  readonly explanation?: string;
}

function setEqual(a: ReadonlySet<string>, b: ReadonlySet<string>): boolean {
  if (a.size !== b.size) return false;
  for (const value of a) if (!b.has(value)) return false;
  return true;
}

function mapEqual(
  a: ReadonlyMap<string, string>,
  b: ReadonlyMap<string, string>,
): boolean {
  if (a.size !== b.size) return false;
  for (const [key, value] of a) if (b.get(key) !== value) return false;
  return true;
}

function compareSuggestions(a: Suggestion, b: Suggestion): number {
  if (a.score !== b.score) return b.score - a.score;
  return a.key < b.key ? -1 : a.key > b.key ? 1 : 0;
}

/**
 * Aggregates candidates pushed by multiple asynchronous sources into one
 * deduplicated, fully sorted suggestion list.
 *
 * - Candidates are deduped by normalized key; the winning score is the max
 *   across contributing sources and each source's explanation is retained.
 * - Every query gets a monotonically increasing generation token; batches
 *   arriving for a stale generation are rejected, so slow responses from a
 *   previous query can never pollute the current one.
 * - A source may retract all of its contributions at any time; winners are
 *   re-selected from the remaining sources.
 * - The highlight is tracked by normalized key (identity), not by list
 *   index, so it survives re-sorts and retractions whenever the key itself
 *   survives. Suggestion objects for unchanged keys keep object identity
 *   across recomputations.
 */
export class SuggestionEngine {
  readonly #normalize: NormalizeFn;
  #generation = 0;
  #query = '';
  /** normalized key -> source id -> contribution */
  readonly #contributions = new Map<string, Map<string, Contribution>>();
  /** normalized key -> last emitted Suggestion, for identity reuse */
  readonly #identityCache = new Map<string, Suggestion>();
  #highlightedKey: string | null = null;

  constructor(options: { normalize?: NormalizeFn } = {}) {
    this.#normalize = options.normalize ?? defaultNormalize;
  }

  /** Current generation token. */
  get generation(): number {
    return this.#generation;
  }

  /** Query string of the current generation. */
  get query(): string {
    return this.#query;
  }

  /**
   * Starts a new query, invalidating every previous generation and dropping
   * all accumulated contributions. Returns the new generation token that
   * sources must echo back in {@link applyBatch}.
   */
  beginQuery(query: string): number {
    this.#query = query;
    this.#generation += 1;
    this.#contributions.clear();
    this.#identityCache.clear();
    this.#highlightedKey = null;
    return this.#generation;
  }

  /**
   * Applies a batch of candidates from one source for one generation.
   * Replaces any contributions the same source made earlier in this
   * generation. Returns false (and changes nothing) when the generation is
   * stale.
   */
  applyBatch(
    generation: number,
    sourceId: string,
    candidates: readonly Candidate[],
  ): boolean {
    if (generation !== this.#generation) return false;
    this.#removeSource(sourceId);
    for (const candidate of candidates) {
      const key = this.#normalize(candidate.key);
      if (key === '') continue;
      let bySource = this.#contributions.get(key);
      if (bySource === undefined) {
        bySource = new Map();
        this.#contributions.set(key, bySource);
      }
      const existing = bySource.get(sourceId);
      if (existing === undefined || candidate.score > existing.score) {
        bySource.set(sourceId, {
          score: candidate.score,
          explanation: candidate.explanation,
        });
      }
    }
    return true;
  }

  /**
   * Withdraws every contribution of a source. Winners for affected keys are
   * re-selected from the remaining sources on the next read.
   */
  retractSource(sourceId: string): void {
    this.#removeSource(sourceId);
  }

  #removeSource(sourceId: string): void {
    for (const [key, bySource] of this.#contributions) {
      if (bySource.delete(sourceId) && bySource.size === 0) {
        this.#contributions.delete(key);
      }
    }
  }

  /**
   * Full aggregation of the current generation, sorted by score descending
   * with a deterministic key tie-break. Keys whose merged value did not
   * change since the last call keep the same object reference.
   */
  suggestions(): Suggestion[] {
    const result: Suggestion[] = [];
    const alive = new Set<string>();
    for (const [key, bySource] of this.#contributions) {
      alive.add(key);
      result.push(this.#materialize(key, bySource));
    }
    for (const key of this.#identityCache.keys()) {
      if (!alive.has(key)) this.#identityCache.delete(key);
    }
    result.sort(compareSuggestions);
    return result;
  }

  #materialize(key: string, bySource: Map<string, Contribution>): Suggestion {
    let score = -Infinity;
    const sources = new Set<string>();
    const explanations = new Map<string, string>();
    for (const [sourceId, contribution] of bySource) {
      sources.add(sourceId);
      if (contribution.score > score) score = contribution.score;
      if (contribution.explanation !== undefined) {
        explanations.set(sourceId, contribution.explanation);
      }
    }
    const cached = this.#identityCache.get(key);
    if (
      cached !== undefined &&
      cached.score === score &&
      setEqual(cached.sources, sources) &&
      mapEqual(cached.explanations, explanations)
    ) {
      return cached;
    }
    const suggestion: Suggestion = { key, score, sources, explanations };
    this.#identityCache.set(key, suggestion);
    return suggestion;
  }

  /**
   * Highlights a suggestion by (unnormalized) key. Returns the highlighted
   * suggestion, or null when the key is not present.
   */
  highlight(key: string): Suggestion | null {
    const normalized = this.#normalize(key);
    if (!this.#contributions.has(normalized)) return null;
    this.#highlightedKey = normalized;
    return this.highlighted();
  }

  /** Removes the highlight. */
  clearHighlight(): void {
    this.#highlightedKey = null;
  }

  /**
   * Moves the highlight by `delta` positions in the current sorted list,
   * clamped to the list bounds. With no active highlight, a positive delta
   * selects the first entry and a negative one the last.
   */
  moveHighlight(delta: number): Suggestion | null {
    const list = this.suggestions();
    if (list.length === 0) return null;
    const current =
      this.#highlightedKey === null
        ? -1
        : list.findIndex((entry) => entry.key === this.#highlightedKey);
    const nextIndex =
      current === -1
        ? delta >= 0
          ? 0
          : list.length - 1
        : Math.min(list.length - 1, Math.max(0, current + delta));
    this.#highlightedKey = list[nextIndex].key;
    return list[nextIndex];
  }

  /**
   * The currently highlighted suggestion, or null when nothing is
   * highlighted or the highlighted key is temporarily absent. The key is
   * remembered, so the highlight resumes if the key reappears within the
   * same generation.
   */
  highlighted(): Suggestion | null {
    if (this.#highlightedKey === null) return null;
    const bySource = this.#contributions.get(this.#highlightedKey);
    if (bySource === undefined) return null;
    return this.#materialize(this.#highlightedKey, bySource);
  }
}
