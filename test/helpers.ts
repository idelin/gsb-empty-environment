import type { Batch } from '../src/core.ts';
import { DELETE_POLICY, Store } from '../src/store.ts';

export const SOURCES = [
  { id: 'erp', priority: 10 },
  { id: 'crm', priority: 20 },
  { id: 'web', priority: 20 },
  { id: 'hr', priority: 30 },
];

export function makeStore(): Store {
  return new Store({ sources: SOURCES, onParentDelete: DELETE_POLICY });
}

let seq = 0;
export function upsert(partial: {
  source: string;
  entity: string;
  version?: number;
  parent?: string | null;
  fields?: Record<string, unknown>;
  eventId?: string;
  table?: string;
}): Batch['events'][number] {
  seq += 1;
  const version = partial.version ?? 1;
  return {
    kind: 'upsert',
    eventId: partial.eventId ?? `ev-${seq}`,
    source: partial.source,
    table: partial.table ?? 't',
    entity: partial.entity,
    version,
    ...(partial.parent !== undefined ? { parent: partial.parent } : {}),
    ...(partial.fields !== undefined
      ? { fields: partial.fields as Record<string, import('../src/core.ts').JsonValue> }
      : {}),
    path: `$.test.events[${seq}]`,
  };
}

export function del(partial: {
  source: string;
  entity: string;
  version?: number;
  eventId?: string;
}): Batch['events'][number] {
  seq += 1;
  return {
    kind: 'delete',
    eventId: partial.eventId ?? `ev-${seq}`,
    source: partial.source,
    table: 't',
    entity: partial.entity,
    version: partial.version ?? 1,
    path: `$.test.events[${seq}]`,
  };
}

export function batchOf(...events: Batch['events']): Batch {
  seq += 1;
  return { id: `batch-${seq}`, events };
}
