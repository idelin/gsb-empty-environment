export type EntityKey = string;

export const keyOf = (typename: string, id: string | number): EntityKey =>
  `${typename}:${String(id)}`;

export const splitKey = (key: EntityKey): { typename: string; id: string } => {
  const idx = key.indexOf(':');
  return { typename: key.slice(0, idx), id: key.slice(idx + 1) };
};

export interface Ref {
  $ref: EntityKey;
}

export const isRef = (value: unknown): value is Ref =>
  typeof value === 'object' &&
  value !== null &&
  typeof (value as { $ref?: unknown }).$ref === 'string';

export interface PageInfo {
  endCursor: string | null;
  hasNextPage: boolean;
}

export interface Connection {
  kind: 'connection';
  refs: Ref[];
  pageInfo: PageInfo;
}

export const isConnection = (value: unknown): value is Connection =>
  typeof value === 'object' &&
  value !== null &&
  (value as { kind?: unknown }).kind === 'connection';

export type Embedded = { [field: string]: Value };

export type Value =
  | string
  | number
  | boolean
  | null
  | Ref
  | Connection
  | Embedded
  | Value[];

export interface FieldEntry {
  value: Value;
  version: number;
}

export interface EntityRecord {
  key: EntityKey;
  typename: string;
  id: string;
  fields: Map<string, FieldEntry>;
  pendingDelete: boolean;
}

export type Op =
  | { op: 'writeField'; key: EntityKey; field: string; value: Value; version: number }
  | { op: 'deleteEntities'; keys: EntityKey[] }
  | { op: 'mergeIdentity'; typename: string; fromId: string; toId: string; version: number }
  | { op: 'acquireLease'; leaseId: string; keys: EntityKey[] }
  | { op: 'releaseLease'; leaseId: string }
  | { op: 'tick' };
