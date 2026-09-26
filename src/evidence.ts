// Sample store with provenance. Evidence is always derived from the RETAINED
// sample set, so retracting an outlier shrinks observed ranges again, and
// every count stays traceable to the source sample ids that produced it.

import { joinInstancePath } from "./validate.ts";

const DISTINCT_STRING_CAP = 50;

export class SampleStore {
  private samples = new Map<string, unknown>();
  private retracted = new Set<string>();

  add(id: string, value: unknown): void {
    if (this.samples.has(id)) throw new Error(`duplicate sample id: ${id}`);
    this.samples.set(id, value);
    this.retracted.delete(id);
  }

  retract(id: string): boolean {
    if (!this.samples.delete(id)) return false;
    this.retracted.add(id);
    return true;
  }

  retained(): [string, unknown][] {
    return [...this.samples.entries()];
  }

  retainedIds(): string[] {
    return [...this.samples.keys()];
  }

  retractedIds(): string[] {
    return [...this.retracted];
  }

  get size(): number {
    return this.samples.size;
  }
}

export interface ValueBound {
  value: number;
  sampleIds: string[]; // provenance: which retained samples attain the bound
}

export interface EvidenceNode {
  path: string;
  present: number;
  missing: number; // object instances of the parent that lacked this key
  nullCount: number;
  typeCounts: Record<string, number>; // string|integer|decimal|boolean|object|array
  numericMin?: ValueBound;
  numericMax?: ValueBound;
  minLengthBound?: ValueBound;
  maxLengthBound?: ValueBound;
  minItemsBound?: ValueBound;
  maxItemsBound?: ValueBound;
  distinctStrings: Map<string, number>;
  stringsTruncated: boolean;
  children: Map<string, EvidenceNode>; // object keys
  items?: EvidenceNode; // merged array-element evidence (path + "[]")
  sampleIds: { present: string[]; missing: string[]; null: string[] };
  objectSampleIds: string[]; // samples where this node was an object
}

function newNode(path: string): EvidenceNode {
  return {
    path,
    present: 0,
    missing: 0,
    nullCount: 0,
    typeCounts: {},
    distinctStrings: new Map(),
    stringsTruncated: false,
    children: new Map(),
    sampleIds: { present: [], missing: [], null: [] },
    objectSampleIds: [],
  };
}

function bump(map: Record<string, number>, key: string): void {
  map[key] = (map[key] ?? 0) + 1;
}

function updateBound(
  bound: ValueBound | undefined,
  value: number,
  sampleId: string,
  keep: "min" | "max",
): ValueBound {
  const better = bound === undefined || (keep === "min" ? value < bound.value : value > bound.value);
  if (better) return { value, sampleIds: [sampleId] };
  if (value === bound.value && !bound.sampleIds.includes(sampleId)) bound.sampleIds.push(sampleId);
  return bound;
}

function observe(node: EvidenceNode, value: unknown, sampleId: string): void {
  node.present++;
  node.sampleIds.present.push(sampleId);

  if (value === null) {
    node.nullCount++;
    node.sampleIds.null.push(sampleId);
    return;
  }
  if (Array.isArray(value)) {
    bump(node.typeCounts, "array");
    node.minItemsBound = updateBound(node.minItemsBound, value.length, sampleId, "min");
    node.maxItemsBound = updateBound(node.maxItemsBound, value.length, sampleId, "max");
    if (value.length > 0) {
      node.items ??= newNode(`${node.path}[]`);
      for (const el of value) observe(node.items, el, sampleId);
    }
    return;
  }
  switch (typeof value) {
    case "object": {
      bump(node.typeCounts, "object");
      if (!node.objectSampleIds.includes(sampleId)) node.objectSampleIds.push(sampleId);
      const obj = value as Record<string, unknown>;
      for (const key of Object.keys(obj)) {
        let child = node.children.get(key);
        if (!child) {
          child = newNode(joinInstancePath(node.path, key));
          node.children.set(key, child);
        }
        observe(child, obj[key], sampleId);
      }
      return;
    }
    case "number":
      bump(node.typeCounts, Number.isInteger(value) ? "integer" : "decimal");
      node.numericMin = updateBound(node.numericMin, value, sampleId, "min");
      node.numericMax = updateBound(node.numericMax, value, sampleId, "max");
      return;
    case "string": {
      bump(node.typeCounts, "string");
      node.minLengthBound = updateBound(node.minLengthBound, value.length, sampleId, "min");
      node.maxLengthBound = updateBound(node.maxLengthBound, value.length, sampleId, "max");
      const seen = node.distinctStrings.get(value);
      if (seen !== undefined) {
        node.distinctStrings.set(value, seen + 1);
      } else if (node.distinctStrings.size < DISTINCT_STRING_CAP) {
        node.distinctStrings.set(value, 1);
      } else {
        node.stringsTruncated = true;
      }
      return;
    }
    case "boolean":
      bump(node.typeCounts, "boolean");
      return;
  }
}

// Recompute the evidence tree from the currently retained samples.
export function buildEvidence(store: SampleStore): EvidenceNode | undefined {
  if (store.size === 0) return undefined;
  const root = newNode("$");
  for (const [id, sample] of store.retained()) observe(root, sample, id);
  // missing = parent object instances minus child presence (missing != null)
  const fillMissing = (node: EvidenceNode): void => {
    const objectInstances = node.typeCounts["object"] ?? 0;
    for (const child of node.children.values()) {
      child.missing = objectInstances - child.present;
      fillMissing(child);
    }
    if (node.items) fillMissing(node.items);
  };
  fillMissing(root);
  // provenance for missing counts
  const fillMissingIds = (node: EvidenceNode): void => {
    for (const child of node.children.values()) {
      const presentIds = new Set(child.sampleIds.present);
      child.sampleIds.missing = node.objectSampleIds.filter((id) => !presentIds.has(id));
      fillMissingIds(child);
    }
    if (node.items) fillMissingIds(node.items);
  };
  fillMissingIds(root);
  return root;
}

// JSON-safe snapshot for reports.
export function snapshotEvidence(node: EvidenceNode): unknown {
  const out: Record<string, unknown> = {
    path: node.path,
    present: node.present,
    missing: node.missing,
    null: node.nullCount,
    types: node.typeCounts,
    provenance: node.sampleIds,
  };
  if (node.numericMin) out.numericMin = node.numericMin;
  if (node.numericMax) out.numericMax = node.numericMax;
  if (node.minLengthBound) out.minLength = node.minLengthBound;
  if (node.maxLengthBound) out.maxLength = node.maxLengthBound;
  if (node.minItemsBound) out.minItems = node.minItemsBound;
  if (node.maxItemsBound) out.maxItems = node.maxItemsBound;
  if (node.distinctStrings.size > 0) {
    out.distinctStrings = Object.fromEntries(node.distinctStrings);
    if (node.stringsTruncated) out.distinctStringsTruncated = true;
  }
  if (node.children.size > 0) {
    out.properties = Object.fromEntries(
      [...node.children.entries()].map(([k, c]) => [k, snapshotEvidence(c)]),
    );
  }
  if (node.items) out.items = snapshotEvidence(node.items);
  return out;
}
