import type { TraceEvent, EventKind } from './types';

export const MEMBERS: string[] = ['n1', 'n2', 'n3', 'n4', 'n5'];
export const QUORUM: number = 3;
export const KINDS: EventKind[] = ['promise', 'accept', 'persist', 'restart', 'learn', 'apply'];
export const BALLOT_KINDS: EventKind[] = ['promise', 'accept', 'persist', 'learn'];

export function isValidBallot(ballot: number): boolean {
  return typeof ballot === 'number' && Number.isInteger(ballot) && ballot >= 0;
}

function isInt(n: unknown): boolean {
  return typeof n === 'number' && Number.isInteger(n);
}

export function validateEvent(raw: any): string[] {
  const problems: string[] = [];
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) {
    problems.push('event must be an object');
    return problems;
  }
  if (typeof raw.id !== 'string' || raw.id.length === 0) problems.push('id must be a non-empty string');
  if (KINDS.indexOf(raw.kind) < 0) problems.push('kind must be one of ' + KINDS.join('/'));
  if (typeof raw.node !== 'string' || raw.node.length === 0) problems.push('node must be a non-empty string');
  if (!isInt(raw.epoch) || raw.epoch < 0) problems.push('epoch must be a non-negative integer');
  if (typeof raw.ballot !== 'number') problems.push('ballot must be a number');
  if (!isInt(raw.slot) || raw.slot < 0) problems.push('slot must be a non-negative integer');
  if (raw.value !== null && raw.value !== undefined && typeof raw.value !== 'string') {
    problems.push('value must be a string digest or null');
  }
  if (raw.causes !== undefined) {
    if (!Array.isArray(raw.causes) || raw.causes.some((c: any) => typeof c !== 'string')) {
      problems.push('causes must be an array of event id strings');
    }
  }
  if (problems.length > 0) return problems;
  const kind: EventKind = raw.kind;
  const needsSlotValue = kind === 'accept' || kind === 'learn' || kind === 'apply';
  if (needsSlotValue && raw.slot < 1) problems.push(kind + ' requires slot >= 1');
  if (needsSlotValue && (raw.value === null || raw.value === undefined)) {
    problems.push(kind + ' requires a value digest');
  }
  if (kind === 'persist' && raw.value !== null && raw.value !== undefined && raw.slot < 1) {
    problems.push('persist with an accepted value requires slot >= 1');
  }
  return problems;
}

export function normalizeEvent(raw: any): TraceEvent {
  return {
    id: raw.id,
    kind: raw.kind,
    node: raw.node,
    epoch: raw.epoch,
    ballot: raw.ballot,
    slot: raw.slot,
    value: raw.value === undefined ? null : raw.value,
    causes: raw.causes === undefined ? [] : raw.causes.slice(),
  };
}

export function canonical(e: TraceEvent): string {
  const causes = e.causes.slice().sort();
  return JSON.stringify([e.id, e.kind, e.node, e.epoch, e.ballot, e.slot, e.value, causes]);
}
