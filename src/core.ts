// Core types, JSON helpers and path-locating errors.
// Input is a JSON-compatible tree: object key order is insignificant,
// array order is significant. All comparisons below honor that.

export type JsonValue =
  | string
  | number
  | boolean
  | null
  | JsonValue[]
  | { [key: string]: JsonValue };

// The parent relation is modeled as a regular field so it inherits
// source priority, versioning and pending-conflict semantics.
export const PARENT_FIELD = '_parent';

export class GraphError extends Error {
  readonly code: string;
  readonly path: string;
  constructor(code: string, message: string, path: string) {
    super(message);
    this.name = 'GraphError';
    this.code = code;
    this.path = path;
  }
}

// Deep equality for JSON values: object key order ignored, array order kept.
export function jsonEquals(a: JsonValue, b: JsonValue): boolean {
  if (a === b) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) {
    return false;
  }
  const aIsArray = Array.isArray(a);
  const bIsArray = Array.isArray(b);
  if (aIsArray !== bIsArray) return false;
  if (aIsArray && bIsArray) {
    if (a.length !== b.length) return false;
    return a.every((item, index) => jsonEquals(item, (b as JsonValue[])[index]));
  }
  const aObj = a as Record<string, JsonValue>;
  const bObj = b as Record<string, JsonValue>;
  const aKeys = Object.keys(aObj);
  const bKeys = Object.keys(bObj);
  if (aKeys.length !== bKeys.length) return false;
  return aKeys.every((key) => key in bObj && jsonEquals(aObj[key], bObj[key]));
}

export interface Contribution {
  source: string;
  version: number;
  value: JsonValue;
  path: string; // JSON path of the value inside the input document
}

export interface FieldState {
  contributions: Map<string, Contribution>; // one per source
  effective: Contribution | null; // null while pending or empty
  pending: Contribution[] | null; // set when top-priority sources disagree
}

export interface EntityNode {
  id: string;
  presence: Map<string, { version: number; path: string }>; // per source
  fields: Map<string, FieldState>;
  parent: string | null; // effective parent id; may reference a missing entity (orphan)
  children: Set<string>; // attached children only
  subtreeSize: number; // aggregate: nodes in subtree incl. self
  subtreeConflicts: number; // aggregate: nodes with pending fields in subtree
  conflict: boolean; // own pending-field flag
}

export interface UpsertEvent {
  kind: 'upsert';
  eventId: string;
  source: string;
  table: string;
  entity: string;
  version: number;
  parent?: string | null; // undefined = this event makes no parent claim
  fields?: Record<string, JsonValue>;
  path: string; // JSON path of this event inside the input document
}

export interface DeleteEvent {
  kind: 'delete';
  eventId: string;
  source: string;
  table: string;
  entity: string;
  version: number;
  path: string;
}

export type Event = UpsertEvent | DeleteEvent;

export interface Batch {
  id: string;
  events: Event[]; // ordered; applied in array order
}

export interface SourceConfig {
  id: string;
  priority: number; // higher wins
}

export interface AppliedError {
  code: string;
  message: string;
  path: string;
  eventId: string;
  batch: string;
}
