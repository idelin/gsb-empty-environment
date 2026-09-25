export type JsonPrimitive = string | number | boolean | null;
export type JsonValue = JsonPrimitive | JsonValue[] | { [key: string]: JsonValue };
export type NodeKind = 'object' | 'array';
export type Key = string | number;
export type NodeId = number;

export type Slot =
  | { t: 'prim'; v: JsonPrimitive }
  | { t: 'child'; node: NodeId }
  | { t: 'ref'; refId: string; expect?: NodeKind };

export interface BackRef {
  from: NodeId;
  key: Key;
  via: 'child' | 'ref';
}

export interface GNode {
  id: NodeId;
  kind: NodeKind;
  idName?: string;
  slots: Map<Key, Slot>;
  inbound: BackRef[];
}

export function isJsonObject(v: JsonValue): v is { [key: string]: JsonValue } {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}
