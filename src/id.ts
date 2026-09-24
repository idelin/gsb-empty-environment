/** Stable, globally unique element identifier. */
export interface ElementId {
  readonly replica: string;
  readonly counter: number;
}

export function idToString(id: ElementId): string {
  return `${id.replica}:${id.counter}`;
}

/** Total order: counter first, then replica id. Deterministic on every replica. */
export function compareIds(a: ElementId, b: ElementId): number {
  if (a.counter !== b.counter) return a.counter - b.counter;
  return a.replica < b.replica ? -1 : a.replica > b.replica ? 1 : 0;
}
