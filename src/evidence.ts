export type Json = null | boolean | number | string | Json[] | { [key: string]: Json };

export const OBSERVED_TYPES = ['integer', 'number', 'string', 'boolean', 'array', 'object'] as const;
export type ObservedType = (typeof OBSERVED_TYPES)[number];

export interface NumericBound {
  value: number;
  sampleId: string;
}

export interface FieldEvidence {
  path: string;
  present: number;
  missing: number;
  nullCount: number;
  typeCounts: Record<ObservedType, number>;
  numericMin: NumericBound | null;
  numericMax: NumericBound | null;
  minLength: number | null;
  maxLength: number | null;
}

export function classify(value: Json): ObservedType | 'null' {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  switch (typeof value) {
    case 'number':
      return Number.isInteger(value) ? 'integer' : 'number';
    case 'string':
      return 'string';
    case 'boolean':
      return 'boolean';
    default:
      return 'object';
  }
}

export function escapeSegment(segment: string): string {
  return segment.replace(/~/g, '~0').replace(/\//g, '~1');
}

export function unescapeSegment(segment: string): string {
  return segment.replace(/~1/g, '/').replace(/~0/g, '~');
}

export class SampleSet {
  private readonly samples = new Map<string, Json>();

  add(id: string, value: Json): void {
    if (this.samples.has(id)) throw new Error(`duplicate sample id: ${id}`);
    this.samples.set(id, value);
  }

  remove(id: string): boolean {
    return this.samples.delete(id);
  }

  get size(): number {
    return this.samples.size;
  }

  entries(): IterableIterator<[string, Json]> {
    return this.samples.entries();
  }

  ids(): string[] {
    return [...this.samples.keys()];
  }
}

export interface EvidenceResult {
  fields: Map<string, FieldEvidence>;
  objectCounts: Map<string, number>;
}

function freshEvidence(path: string): FieldEvidence {
  return {
    path,
    present: 0,
    missing: 0,
    nullCount: 0,
    typeCounts: { integer: 0, number: 0, string: 0, boolean: 0, array: 0, object: 0 },
    numericMin: null,
    numericMax: null,
    minLength: null,
    maxLength: null,
  };
}

function parentPath(path: string): string {
  const idx = path.lastIndexOf('/');
  return idx <= 0 ? '' : path.slice(0, idx);
}

function record(path: string, value: Json, sampleId: string, fields: Map<string, FieldEvidence>): void {
  let field = fields.get(path);
  if (!field) {
    field = freshEvidence(path);
    fields.set(path, field);
  }
  field.present += 1;
  if (value === null) {
    field.nullCount += 1;
    return;
  }
  const kind = classify(value);
  if (kind === 'null') return;
  field.typeCounts[kind] += 1;
  if (kind === 'integer' || kind === 'number') {
    const num = value as number;
    if (!field.numericMin || num < field.numericMin.value) field.numericMin = { value: num, sampleId };
    if (!field.numericMax || num > field.numericMax.value) field.numericMax = { value: num, sampleId };
  } else if (kind === 'string') {
    const len = (value as string).length;
    field.minLength = field.minLength === null ? len : Math.min(field.minLength, len);
    field.maxLength = field.maxLength === null ? len : Math.max(field.maxLength, len);
  }
}

function walk(
  value: Json,
  path: string,
  sampleId: string,
  fields: Map<string, FieldEvidence>,
  objectCounts: Map<string, number>,
): void {
  if (value === null || typeof value !== 'object') return;
  if (Array.isArray(value)) {
    for (const item of value) {
      const itemPath = path + '[]';
      record(itemPath, item, sampleId, fields);
      walk(item, itemPath, sampleId, fields, objectCounts);
    }
    return;
  }
  objectCounts.set(path, (objectCounts.get(path) ?? 0) + 1);
  for (const [key, child] of Object.entries(value)) {
    const childPath = path + '/' + escapeSegment(key);
    record(childPath, child, sampleId, fields);
    walk(child, childPath, sampleId, fields, objectCounts);
  }
}

export function collectEvidence(samples: Iterable<[string, Json]>): EvidenceResult {
  const fields = new Map<string, FieldEvidence>();
  const objectCounts = new Map<string, number>();
  for (const [id, value] of samples) {
    record('', value, id, fields);
    walk(value, '', id, fields, objectCounts);
  }
  for (const [path, field] of fields) {
    if (path === '' || path.endsWith('[]')) continue;
    field.missing = (objectCounts.get(parentPath(path)) ?? 0) - field.present;
  }
  return { fields, objectCounts };
}
