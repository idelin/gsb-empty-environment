export type JsonValue =
  | string
  | number
  | boolean
  | null
  | JsonValue[]
  | { [key: string]: JsonValue };

export type JsonObject = { [key: string]: JsonValue };

export type Path = readonly string[];

export interface SetOperation {
  kind: 'set';
  path: Path;
  value: JsonValue;
  version: number;
  clientId: string;
}

export interface DeleteOperation {
  kind: 'delete';
  path: Path;
  version: number;
  clientId: string;
}

export type Operation = SetOperation | DeleteOperation;

interface Stamp {
  version: number;
  clientId: string;
}

const TOMBSTONE: unique symbol = Symbol('tombstone');
type LeafValue = JsonValue | typeof TOMBSTONE;

interface Entry extends Stamp {
  value: LeafValue;
}

export interface SnapshotEntry {
  path: string[];
  version: number;
  clientId: string;
  tombstone: boolean;
  value: JsonValue;
}

export interface Snapshot {
  clientId: string;
  clock: number;
  entries: SnapshotEntry[];
}

function compareStamp(a: Stamp, b: Stamp): number {
  if (a.version !== b.version) return a.version - b.version;
  if (a.clientId === b.clientId) return 0;
  return a.clientId < b.clientId ? -1 : 1;
}

function isPlainObject(value: JsonValue): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function pathKey(path: Path): string {
  return JSON.stringify(path);
}

function expandLeaves(path: Path, value: JsonValue): Array<[string[], JsonValue]> {
  if (isPlainObject(value) && Object.keys(value).length > 0) {
    const leaves: Array<[string[], JsonValue]> = [];
    for (const key of Object.keys(value)) {
      leaves.push(...expandLeaves([...path, key], value[key]));
    }
    return leaves;
  }
  return [[[...path], value]];
}

function assignPath(root: JsonValue, path: readonly string[], value: JsonValue): JsonValue {
  if (path.length === 0) return value;
  const container: JsonObject = isPlainObject(root) ? root : {};
  let node = container;
  for (let i = 0; i < path.length - 1; i += 1) {
    const next = node[path[i]];
    if (!isPlainObject(next)) {
      node[path[i]] = {};
    }
    node = node[path[i]] as JsonObject;
  }
  node[path[path.length - 1]] = value;
  return container;
}

function isPrefixPath(prefix: readonly string[], path: readonly string[]): boolean {
  if (prefix.length > path.length) return false;
  for (let i = 0; i < prefix.length; i += 1) {
    if (prefix[i] !== path[i]) return false;
  }
  return true;
}

export class Store {
  readonly clientId: string;
  private clock: number;
  private readonly entries = new Map<string, { path: string[]; entry: Entry }>();

  constructor(clientId: string, clock = 0) {
    this.clientId = clientId;
    this.clock = clock;
  }

  get version(): number {
    return this.clock;
  }

  set(path: Path, value: JsonValue): SetOperation {
    const op: SetOperation = {
      kind: 'set',
      path: [...path],
      value,
      version: ++this.clock,
      clientId: this.clientId,
    };
    this.apply(op);
    return op;
  }

  delete(path: Path): DeleteOperation {
    const op: DeleteOperation = {
      kind: 'delete',
      path: [...path],
      version: ++this.clock,
      clientId: this.clientId,
    };
    this.apply(op);
    return op;
  }

  apply(op: Operation): void {
    if (op.version > this.clock) this.clock = op.version;
    if (op.kind === 'delete') {
      this.putLeaf(op.path, { version: op.version, clientId: op.clientId, value: TOMBSTONE });
      return;
    }
    for (const [leafPath, leafValue] of expandLeaves(op.path, op.value)) {
      this.putLeaf(leafPath, { version: op.version, clientId: op.clientId, value: leafValue });
    }
  }

  get(path: Path = []): JsonValue | undefined {
    let node: JsonValue | undefined = this.materialize();
    for (const key of path) {
      if (!isPlainObject(node as JsonValue)) return undefined;
      node = (node as JsonObject)[key];
    }
    return node;
  }

  materialize(): JsonValue {
    let root: JsonValue = {};
    for (const record of this.entries.values()) {
      if (record.entry.value === TOMBSTONE) continue;
      if (!this.isVisible(record)) continue;
      root = assignPath(root, record.path, structuredClone(record.entry.value));
    }
    return root;
  }

  snapshot(): Snapshot {
    const entries: SnapshotEntry[] = [];
    for (const { path, entry } of this.entries.values()) {
      entries.push({
        path: [...path],
        version: entry.version,
        clientId: entry.clientId,
        tombstone: entry.value === TOMBSTONE,
        value: entry.value === TOMBSTONE ? null : structuredClone(entry.value),
      });
    }
    return { clientId: this.clientId, clock: this.clock, entries };
  }

  static restore(snapshot: Snapshot): Store {
    const store = new Store(snapshot.clientId, snapshot.clock);
    for (const entry of snapshot.entries) {
      store.putLeaf(entry.path, {
        version: entry.version,
        clientId: entry.clientId,
        value: entry.tombstone ? TOMBSTONE : structuredClone(entry.value),
      });
    }
    return store;
  }

  private putLeaf(path: Path, candidate: Entry): void {
    const key = pathKey(path);
    const existing = this.entries.get(key);
    if (existing && compareStamp(existing.entry, candidate) >= 0) return;
    this.entries.set(key, { path: [...path], entry: candidate });
  }

  private isVisible(record: { path: readonly string[]; entry: Entry }): boolean {
    for (const other of this.entries.values()) {
      if (other === record) continue;
      if (compareStamp(other.entry, record.entry) <= 0) continue;
      if (isPrefixPath(other.path, record.path) || isPrefixPath(record.path, other.path)) {
        return false;
      }
    }
    return true;
  }
}
