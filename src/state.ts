import { digestOf } from './hash.ts';

export type JsonValue = null | boolean | number | string | JsonValue[] | { [k: string]: JsonValue };

export type PatchOp =
  | { op: 'set'; key: string; value: JsonValue }
  | { op: 'del'; key: string };

export type Patch = PatchOp[];

/** Committed key-value state. Only mutated by whole patches, never exposed live. */
export class StateStore {
  private data = new Map<string, JsonValue>();

  static from(snapshot: Record<string, JsonValue>): StateStore {
    const s = new StateStore();
    for (const [k, v] of Object.entries(snapshot)) s.data.set(k, structuredClone(v));
    return s;
  }

  has(key: string): boolean {
    return this.data.has(key);
  }

  get(key: string): JsonValue | undefined {
    const v = this.data.get(key);
    return v === undefined ? undefined : structuredClone(v);
  }

  apply(patch: Patch): void {
    for (const p of patch) {
      if (p.op === 'set') this.data.set(p.key, structuredClone(p.value));
      else this.data.delete(p.key);
    }
  }

  /** Inverse of `patch` relative to the state *before* it is applied. */
  invert(patch: Patch): Patch {
    const inverse: Patch = [];
    for (let i = patch.length - 1; i >= 0; i--) {
      const p = patch[i];
      if (p.op === 'set') {
        inverse.push(this.data.has(p.key)
          ? { op: 'set', key: p.key, value: structuredClone(this.data.get(p.key)!) }
          : { op: 'del', key: p.key });
      } else {
        inverse.push(this.data.has(p.key)
          ? { op: 'set', key: p.key, value: structuredClone(this.data.get(p.key)!) }
          : { op: 'del', key: p.key });
      }
    }
    return inverse;
  }

  /** Deep-frozen snapshot; readers can never observe or mutate live state. */
  snapshot(): Record<string, JsonValue> {
    const out: Record<string, JsonValue> = {};
    for (const k of [...this.data.keys()].sort()) out[k] = structuredClone(this.data.get(k)!);
    return Object.freeze(out);
  }

  digest(): string {
    return digestOf(this.snapshot());
  }
}
