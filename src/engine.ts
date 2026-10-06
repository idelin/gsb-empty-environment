import { SuggestionMerger } from "./merger.ts";
import type { Candidate, Suggestion } from "./merger.ts";

export interface SuggestionSource {
  id: string;
  fetch(query: string, signal: AbortSignal): Promise<Candidate[]>;
}

export interface QueryHandle {
  generation: number;
  settled: Promise<void>;
}

export class SuggestionEngine {
  #merger: SuggestionMerger;
  #sources = new Map<string, SuggestionSource>();
  #generation = 0;
  #controller: AbortController | null = null;
  #onUpdate: (() => void) | null = null;

  constructor(merger: SuggestionMerger = new SuggestionMerger()) {
    this.#merger = merger;
  }

  set onUpdate(listener: (() => void) | null) {
    this.#onUpdate = listener;
  }

  get generation(): number {
    return this.#generation;
  }

  registerSource(source: SuggestionSource): void {
    this.#sources.set(source.id, source);
  }

  unregisterSource(sourceId: string): void {
    this.#sources.delete(sourceId);
    this.withdraw(sourceId);
  }

  query(text: string): QueryHandle {
    this.#controller?.abort();
    const controller = new AbortController();
    this.#controller = controller;
    const generation = ++this.#generation;
    this.#merger.clear();
    this.#emit();

    const pending = [...this.#sources.values()].map((source) =>
      source
        .fetch(text, controller.signal)
        .then((candidates) => {
          this.accept(source.id, generation, candidates);
        })
        .catch((error: unknown) => {
          if (!controller.signal.aborted) throw error;
        }),
    );

    return { generation, settled: Promise.all(pending).then(() => undefined) };
  }

  accept(sourceId: string, generation: number, candidates: Candidate[]): boolean {
    if (generation !== this.#generation) return false;
    this.#merger.replaceSource(sourceId, candidates);
    this.#emit();
    return true;
  }

  withdraw(sourceId: string): void {
    this.#merger.withdrawSource(sourceId);
    this.#emit();
  }

  list(): Suggestion[] {
    return this.#merger.list();
  }

  highlight(id: string | null): Suggestion | null {
    return this.#merger.highlight(id);
  }

  moveHighlight(delta: number): Suggestion | null {
    return this.#merger.moveHighlight(delta);
  }

  highlighted(): Suggestion | null {
    return this.#merger.highlighted();
  }

  #emit(): void {
    this.#onUpdate?.();
  }
}
