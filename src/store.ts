import { Journal } from './journal.ts';
import { deletePath, getPath, setPath } from './path.ts';
import type {
  JournalEntry,
  Op,
  RenameMap,
  State,
  Unsubscribe,
} from './types.ts';
import { deepClone, deepFreeze, nowIso } from './util.ts';

export class Transaction {
  #draft: State;
  #ops: Op[] = [];

  constructor(base: State) {
    this.#draft = deepClone(base);
  }

  get(path: string): unknown {
    return getPath(this.#draft, path);
  }

  set(path: string, value: unknown): void {
    const stored = deepClone(value);
    setPath(this.#draft, path, stored);
    this.#ops.push({ kind: 'set', path, value: deepClone(value) });
  }

  delete(path: string): void {
    if (deletePath(this.#draft, path)) {
      this.#ops.push({ kind: 'delete', path });
    }
  }

  get ops(): readonly Op[] {
    return this.#ops;
  }

  get draft(): State {
    return this.#draft;
  }
}

type Selector = (state: State) => unknown;
type Listener = (next: unknown, previous: unknown) => void;

interface Subscription {
  selector: Selector;
  listener: Listener;
  last: unknown;
  boundPath: string | null;
}

export interface BindingRemap {
  from: string;
  to: string;
}

export class Store {
  #committed: State;
  #version: string;
  #journal = new Journal();
  #txSeq = 0;
  #subSeq = 0;
  #subs = new Map<number, Subscription>();
  #pauseDepth = 0;

  constructor(genesis: State, schemaVersion: string) {
    this.#committed = deepFreeze(deepClone(genesis));
    this.#version = schemaVersion;
  }

  get schemaVersion(): string {
    return this.#version;
  }

  get journal(): Journal {
    return this.#journal;
  }

  get subscriptionsPaused(): boolean {
    return this.#pauseDepth > 0;
  }

  getState(): State {
    return this.#committed;
  }

  get(path: string): unknown {
    return getPath(this.#committed, path);
  }

  select<T>(selector: (state: State) => T): T {
    return selector(this.#committed);
  }

  transaction(fn: (tx: Transaction) => void): void {
    const tx = new Transaction(this.#committed);
    fn(tx);
    if (tx.ops.length === 0) return;
    this.#txSeq += 1;
    this.#commit(tx.draft, {
      type: 'tx',
      id: this.#txSeq,
      at: nowIso(),
      ops: [...tx.ops],
    });
  }

  subscribe<T>(
    selector: (state: State) => T,
    listener: (next: T, previous: T) => void,
  ): Unsubscribe {
    return this.#addSub({
      selector: selector as Selector,
      listener: listener as Listener,
      last: selector(this.#committed),
      boundPath: null,
    });
  }

  bind<T>(
    path: string,
    listener: (next: T, previous: T) => void,
  ): Unsubscribe {
    const selector: Selector = (state) => getPath(state, path);
    return this.#addSub({
      selector,
      listener: listener as Listener,
      last: selector(this.#committed),
      boundPath: path,
    });
  }

  pauseSubscriptions(): void {
    this.#pauseDepth += 1;
  }

  resumeSubscriptions(): void {
    if (this.#pauseDepth === 0) {
      throw new Error('subscriptions are not paused');
    }
    this.#pauseDepth -= 1;
    if (this.#pauseDepth === 0) this.#notify();
  }

  replaceState(next: State, version: string, entry: JournalEntry): void {
    this.#version = version;
    this.#commit(deepClone(next), entry);
  }

  remapBindings(renames: RenameMap): BindingRemap[] {
    const applied: BindingRemap[] = [];
    for (const sub of this.#subs.values()) {
      if (sub.boundPath === null) continue;
      const remapped = remapPath(sub.boundPath, renames);
      if (remapped === sub.boundPath) continue;
      applied.push({ from: sub.boundPath, to: remapped });
      sub.boundPath = remapped;
      sub.selector = (state) => getPath(state, remapped);
    }
    return applied;
  }

  #addSub(sub: Subscription): Unsubscribe {
    this.#subSeq += 1;
    const id = this.#subSeq;
    this.#subs.set(id, sub);
    return () => {
      this.#subs.delete(id);
    };
  }

  #commit(next: State, entry: JournalEntry): void {
    this.#committed = deepFreeze(next);
    this.#journal.append(entry);
    this.#notify();
  }

  #notify(): void {
    if (this.#pauseDepth > 0) return;
    for (const sub of this.#subs.values()) {
      const next = sub.selector(this.#committed);
      if (!Object.is(next, sub.last)) {
        const previous = sub.last;
        sub.last = next;
        sub.listener(next, previous);
      }
    }
  }
}

function remapPath(path: string, renames: RenameMap): string {
  for (const [oldPath, newPath] of Object.entries(renames)) {
    if (path === oldPath) return newPath;
    if (path.startsWith(`${oldPath}.`)) {
      return newPath + path.slice(oldPath.length);
    }
  }
  return path;
}
