import type { TraceEvent } from './types.ts';
import { MEMBER_COUNT } from './types.ts';
import { stableStringify } from './json.ts';

export class TraceValidationError extends Error {}
export class TraceConflictError extends Error {}

export interface IngestedTrace {
  events: TraceEvent[]; // deduplicated, in original order
  byId: Map<string, TraceEvent>;
  indexOf: Map<string, number>; // event id -> position in events
  duplicatesIgnored: number;
}

function isObject(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === 'object' && !Array.isArray(v);
}

function requireString(obj: Record<string, unknown>, field: string, id: string): string {
  const v = obj[field];
  if (typeof v !== 'string' || v.length === 0) {
    throw new TraceValidationError(`event ${id}: field "${field}" must be a non-empty string`);
  }
  return v;
}

function requireNumber(obj: Record<string, unknown>, field: string, id: string): number {
  const v = obj[field];
  if (typeof v !== 'number' || !Number.isFinite(v)) {
    throw new TraceValidationError(`event ${id}: field "${field}" must be a finite number`);
  }
  return v;
}

function validateBallot(v: unknown, id: string, where: string): void {
  if (!isObject(v) || typeof v.round !== 'number' || typeof v.node !== 'string') {
    throw new TraceValidationError(`event ${id}: ${where} must be a ballot {round, node}`);
  }
}

function validateDurableState(v: unknown, id: string, where: string): void {
  if (!isObject(v)) {
    throw new TraceValidationError(`event ${id}: ${where} must be an object`);
  }
  if (v.promised !== undefined) validateBallot(v.promised, id, `${where}.promised`);
  if (v.accepted !== undefined) {
    if (!Array.isArray(v.accepted)) {
      throw new TraceValidationError(`event ${id}: ${where}.accepted must be an array`);
    }
    for (const entry of v.accepted) {
      if (!isObject(entry) || typeof entry.slot !== 'number' || typeof entry.value !== 'string') {
        throw new TraceValidationError(`event ${id}: ${where}.accepted entries need slot and value`);
      }
      validateBallot(entry.ballot, id, `${where}.accepted[].ballot`);
    }
  }
}

/** Structural validation of one raw event. Returns the event typed. */
export function validateEvent(raw: unknown, members: ReadonlySet<string>): TraceEvent {
  if (!isObject(raw)) throw new TraceValidationError('event must be an object');
  const id = requireString(raw, 'id', '(unknown)');
  const kind = requireString(raw, 'kind', id);
  if (!['promise', 'accept', 'persist', 'restart', 'learn', 'apply'].includes(kind)) {
    throw new TraceValidationError(`event ${id}: unknown kind "${kind}"`);
  }
  const node = requireString(raw, 'node', id);
  if (!members.has(node)) {
    throw new TraceValidationError(`event ${id}: node "${node}" is not a cluster member`);
  }
  const epoch = requireNumber(raw, 'epoch', id);
  if (!Array.isArray(raw.causes) || raw.causes.some((c) => typeof c !== 'string')) {
    throw new TraceValidationError(`event ${id}: causes must be an array of event ids`);
  }
  if (raw.ballot !== undefined) validateBallot(raw.ballot, id, 'ballot');
  if (raw.slot !== undefined && typeof raw.slot !== 'number') {
    throw new TraceValidationError(`event ${id}: slot must be a number`);
  }
  if (raw.value !== undefined && typeof raw.value !== 'string') {
    throw new TraceValidationError(`event ${id}: value must be a string digest`);
  }

  const need = (cond: boolean, what: string) => {
    if (!cond) throw new TraceValidationError(`event ${id}: kind "${kind}" requires ${what}`);
  };
  switch (kind) {
    case 'promise':
      need(raw.ballot !== undefined, 'ballot');
      break;
    case 'accept':
      need(raw.ballot !== undefined && raw.slot !== undefined && raw.value !== undefined,
        'ballot, slot and value');
      break;
    case 'persist':
      need(raw.persisted !== undefined, 'persisted state');
      validateDurableState(raw.persisted, id, 'persisted');
      break;
    case 'restart':
      need(raw.recovered !== undefined, 'recovered state');
      validateDurableState(raw.recovered, id, 'recovered');
      break;
    case 'learn':
      need(raw.ballot !== undefined && raw.slot !== undefined && raw.value !== undefined,
        'ballot, slot and value');
      break;
    case 'apply':
      need(raw.slot !== undefined && raw.value !== undefined, 'slot and value');
      break;
  }
  return raw as unknown as TraceEvent;
}

/**
 * Ingests raw events: validates, then deduplicates by id.
 * Same id + identical content is idempotent (dropped); same id + different
 * content is a conflict and the whole trace is rejected.
 */
export function ingestTrace(rawEvents: unknown[], members: string[]): IngestedTrace {
  if (members.length !== MEMBER_COUNT) {
    throw new TraceValidationError(`cluster must have exactly ${MEMBER_COUNT} members`);
  }
  const memberSet = new Set(members);
  if (memberSet.size !== members.length) {
    throw new TraceValidationError('member ids must be distinct');
  }
  const events: TraceEvent[] = [];
  const byId = new Map<string, TraceEvent>();
  const indexOf = new Map<string, number>();
  const canonicalById = new Map<string, string>();
  let duplicatesIgnored = 0;

  for (const raw of rawEvents) {
    const event = validateEvent(raw, memberSet);
    const canonical = stableStringify(event);
    const seen = canonicalById.get(event.id);
    if (seen !== undefined) {
      if (seen === canonical) {
        duplicatesIgnored += 1; // idempotent re-delivery
        continue;
      }
      throw new TraceConflictError(`event id "${event.id}" delivered with conflicting content`);
    }
    canonicalById.set(event.id, canonical);
    indexOf.set(event.id, events.length);
    events.push(event);
    byId.set(event.id, event);
  }
  return { events, byId, indexOf, duplicatesIgnored };
}
