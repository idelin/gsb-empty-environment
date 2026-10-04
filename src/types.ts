export type Key = string;

/** A stored record. value === null means the record is a tombstone (deleted). */
export interface StoredRecord {
  value: string | null;
  version: number;
}

/** A single versioned mutation; the unit of replication. */
export interface Mutation {
  key: Key;
  value: string | null;
  version: number;
  requestId: string;
}

export type OpKind = 'put' | 'delete';

export interface ClientRequest {
  requestId: string;
  op: OpKind;
  key: Key;
  value?: string;
}

export type ResponseStatus = 'ok' | 'fenced' | 'not-active';

export interface ClientResponse {
  requestId: string;
  status: ResponseStatus;
  version?: number;
}

/** A replication message travelling from A to B. */
export interface LinkMessage {
  channel: 'base' | 'delta';
  mutation: Mutation;
}
