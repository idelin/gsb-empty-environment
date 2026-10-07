export type ClientId = string;
export type Path = readonly string[];

export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export interface Stamp {
  version: number;
  clientId: ClientId;
}

export type Operation =
  | { kind: 'set'; path: Path; value: JsonValue; version: number; clientId: ClientId }
  | { kind: 'delete'; path: Path; version: number; clientId: ClientId };

export interface FieldEntrySnapshot {
  path: string[];
  version: number;
  clientId: ClientId;
  tombstone: boolean;
  value?: JsonValue;
}

export interface Snapshot {
  fields: FieldEntrySnapshot[];
}

interface FieldEntry {
  stamp: Stamp;
  tombstone: boolean;
  value?: JsonValue;
}

export function compareStamp(a: Stamp, b: Stamp): number {
  if (a.version !== b.version) return a.version - b.version;
  if (a.clientId === b.clientId) return 0;
  return a.clientId < b.clientId ? -1 : 1;
}

function isPlainObject(value: JsonValue): value is { [key: string]: JsonValue } {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function cloneValue<T extends JsonValue>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

function pathKey(path: Path): string {
  return JSON.stringify(path);
}

function isPrefix(prefix: Path, path: Path): boolean {
  if (prefix.length >= path.length) return false;
  for (let i = 0; i < prefix.length; i++) {
    if (prefix[i] !== path[i]) return false;
  }
  return true;
}

export class LwwDocument {
  private readonly fields = new Map<string, FieldEntry>();

  static create(): LwwDocument {
    return new LwwDocument();
  }

  static restore(snapshot: Snapshot): LwwDocument {
    const doc = new LwwDocument();
    doc.merge(snapshot);
    return doc;
  }

  apply(op: Operation): void {
    const stamp: Stamp = { version: op.version, clientId: op.clientId };
    if (op.kind === 'delete') {
      this.putEntry(op.path, { stamp, tombstone: true });
      return;
    }
    this.applySet(op.path, op.value, stamp);
  }

  private applySet(path: Path, value: JsonValue, stamp: Stamp): void {
    if (isPlainObject(value) && Object.keys(value).length > 0) {
      for (const key of Object.keys(value)) {
        this.applySet([...path, key], value[key], stamp);
      }
      return;
    }
    this.putEntry(path, { stamp, tombstone: false, value: cloneValue(value) });
  }

  private putEntry(path: Path, entry: FieldEntry): void {
    const key = pathKey(path);
    const existing = this.fields.get(key);
    if (!existing || compareStamp(entry.stamp, existing.stamp) > 0) {
      this.fields.set(key, entry);
    }
  }

  merge(source: LwwDocument | Snapshot): void {
    const entries = source instanceof LwwDocument ? source.snapshot().fields : source.fields;
    for (const entry of entries) {
      this.putEntry(entry.path, {
        stamp: { version: entry.version, clientId: entry.clientId },
        tombstone: entry.tombstone,
        value: entry.tombstone ? undefined : cloneValue(entry.value as JsonValue),
      });
    }
  }

  snapshot(): Snapshot {
    const fields: FieldEntrySnapshot[] = [];
    for (const [key, entry] of this.fields) {
      fields.push({
        path: JSON.parse(key) as string[],
        version: entry.stamp.version,
        clientId: entry.stamp.clientId,
        tombstone: entry.tombstone,
        ...(entry.tombstone ? {} : { value: cloneValue(entry.value as JsonValue) }),
      });
    }
    fields.sort((a, b) => {
      const ka = JSON.stringify(a.path);
      const kb = JSON.stringify(b.path);
      return ka < kb ? -1 : ka > kb ? 1 : 0;
    });
    return { fields };
  }

  private isVisible(path: string[], entry: FieldEntry): boolean {
    for (let i = 1; i < path.length; i++) {
      const ancestor = this.fields.get(pathKey(path.slice(0, i)));
      if (ancestor && compareStamp(ancestor.stamp, entry.stamp) >= 0) {
        return false;
      }
    }
    return true;
  }

  value(): JsonValue | undefined {
    const visible: Array<{ path: string[]; entry: FieldEntry }> = [];
    for (const [key, entry] of this.fields) {
      const path = JSON.parse(key) as string[];
      if (this.isVisible(path, entry)) {
        visible.push({ path, entry });
      }
    }

    const leaves = visible.filter(
      (item) =>
        !item.entry.tombstone &&
        !visible.some((other) => isPrefix(item.path, other.path)),
    );

    leaves.sort((a, b) => {
      const ka = JSON.stringify(a.path);
      const kb = JSON.stringify(b.path);
      return ka < kb ? -1 : ka > kb ? 1 : 0;
    });

    if (leaves.length === 0) return undefined;

    for (const leaf of leaves) {
      if (leaf.path.length === 0) {
        return cloneValue(leaf.entry.value as JsonValue);
      }
    }

    const root: { [key: string]: JsonValue } = {};
    for (const leaf of leaves) {
      let node: { [key: string]: JsonValue } = root;
      for (let i = 0; i < leaf.path.length - 1; i++) {
        const segment = leaf.path[i];
        if (!isPlainObject(node[segment])) {
          node[segment] = {};
        }
        node = node[segment] as { [key: string]: JsonValue };
      }
      node[leaf.path[leaf.path.length - 1]] = cloneValue(leaf.entry.value as JsonValue);
    }
    return root;
  }
}
