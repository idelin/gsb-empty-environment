import { deletePath, setPath } from './path.ts';
import type { JournalEntry, Op, State } from './types.ts';
import { deepClone } from './util.ts';

export class Journal {
  #entries: JournalEntry[] = [];

  append(entry: JournalEntry): void {
    this.#entries.push(entry);
  }

  entries(): readonly JournalEntry[] {
    return this.#entries;
  }

  get length(): number {
    return this.#entries.length;
  }
}

export function applyOp(state: State, op: Op): void {
  if (op.kind === 'set') {
    setPath(state, op.path, deepClone(op.value));
  } else {
    deletePath(state, op.path);
  }
}

export function replayJournal(
  genesis: State,
  entries: readonly JournalEntry[],
): State {
  let state = deepClone(genesis);
  for (const entry of entries) {
    if (entry.type === 'tx') {
      for (const op of entry.ops) applyOp(state, op);
    } else {
      state = deepClone(entry.state);
    }
  }
  return state;
}

export function replayVersion(
  genesisVersion: string,
  entries: readonly JournalEntry[],
): string {
  let version = genesisVersion;
  for (const entry of entries) {
    if (entry.type === 'replace') version = entry.toVersion;
  }
  return version;
}
