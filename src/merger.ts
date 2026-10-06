export interface Candidate {
  key: string;
  label?: string;
  score: number;
  detail?: string;
}

export interface Suggestion {
  id: string;
  label: string;
  score: number;
  sources: string[];
  detail?: string;
}

export type KeyNormalizer = (key: string) => string;

export const defaultNormalizeKey: KeyNormalizer = (key) =>
  key.trim().replace(/\s+/g, " ").toLowerCase();

interface Contribution {
  sourceId: string;
  label: string;
  score: number;
  detail?: string;
}

function pickWinner(contributions: Contribution[]): Contribution {
  let winner = contributions[0];
  for (const c of contributions) {
    if (c.score > winner.score) winner = c;
    else if (c.score === winner.score && c.sourceId < winner.sourceId) winner = c;
  }
  return winner;
}

function compareSuggestions(a: Suggestion, b: Suggestion): number {
  if (b.score !== a.score) return b.score - a.score;
  if (a.label !== b.label) return a.label < b.label ? -1 : 1;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

export class SuggestionMerger {
  #entries = new Map<string, Map<string, Contribution>>();
  #normalize: KeyNormalizer;
  #highlightedId: string | null = null;
  #highlightIndex = 0;

  constructor(normalize: KeyNormalizer = defaultNormalizeKey) {
    this.#normalize = normalize;
  }

  replaceSource(sourceId: string, candidates: Candidate[]): Suggestion[] {
    this.#dropSource(sourceId);
    for (const candidate of candidates) {
      const id = this.#normalize(candidate.key);
      if (!id) continue;
      let bucket = this.#entries.get(id);
      if (!bucket) {
        bucket = new Map();
        this.#entries.set(id, bucket);
      }
      const next: Contribution = {
        sourceId,
        label: candidate.label ?? candidate.key.trim(),
        score: candidate.score,
        detail: candidate.detail,
      };
      const prev = bucket.get(sourceId);
      if (!prev || next.score > prev.score) bucket.set(sourceId, next);
    }
    return this.#sync();
  }

  withdrawSource(sourceId: string): Suggestion[] {
    this.#dropSource(sourceId);
    return this.#sync();
  }

  clear(): Suggestion[] {
    this.#entries.clear();
    return this.#sync();
  }

  list(): Suggestion[] {
    const merged: Suggestion[] = [];
    for (const [id, bucket] of this.#entries) {
      const contributions = [...bucket.values()];
      const winner = pickWinner(contributions);
      merged.push({
        id,
        label: winner.label,
        score: winner.score,
        sources: contributions.map((c) => c.sourceId).sort(),
        detail: winner.detail,
      });
    }
    return merged.sort(compareSuggestions);
  }

  highlight(id: string | null): Suggestion | null {
    this.#highlightedId = id;
    if (id !== null) {
      const index = this.list().findIndex((s) => s.id === id);
      if (index >= 0) this.#highlightIndex = index;
    }
    return this.highlighted();
  }

  moveHighlight(delta: number): Suggestion | null {
    const list = this.list();
    if (list.length === 0) return this.highlight(null);
    const current = list.findIndex((s) => s.id === this.#highlightedId);
    const from =
      current >= 0 ? current : delta > 0 ? -1 : list.length;
    const next = Math.min(Math.max(from + delta, 0), list.length - 1);
    return this.highlight(list[next].id);
  }

  highlighted(): Suggestion | null {
    if (this.#highlightedId === null) return null;
    return this.list().find((s) => s.id === this.#highlightedId) ?? null;
  }

  #dropSource(sourceId: string): void {
    for (const [id, bucket] of this.#entries) {
      if (bucket.delete(sourceId) && bucket.size === 0) this.#entries.delete(id);
    }
  }

  #sync(): Suggestion[] {
    const list = this.list();
    if (this.#highlightedId !== null) {
      const index = list.findIndex((s) => s.id === this.#highlightedId);
      if (index >= 0) {
        this.#highlightIndex = index;
        return list;
      }
    }
    if (list.length === 0) {
      this.#highlightedId = null;
      this.#highlightIndex = 0;
      return list;
    }
    this.#highlightIndex = Math.min(this.#highlightIndex, list.length - 1);
    this.#highlightedId = list[this.#highlightIndex].id;
    return list;
  }
}
