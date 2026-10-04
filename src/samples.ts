import type { TraceEvent, EventKind } from './types';

function ev(
  id: string,
  kind: EventKind,
  node: string,
  epoch: number,
  ballot: number,
  slot: number,
  value: string | null,
  causes: string[]
): TraceEvent {
  return { id, kind, node, epoch, ballot, slot, value, causes };
}

// Two different values are each accepted by a quorum at slot 1 and both get
// learned: the classic double-chosen safety violation.
export function doubleChosen(): TraceEvent[] {
  return [
    ev('a1', 'accept', 'n1', 1, 1, 1, 'v:A', []),
    ev('a2', 'accept', 'n2', 1, 1, 1, 'v:A', []),
    ev('a3', 'accept', 'n3', 1, 1, 1, 'v:A', []),
    ev('l1', 'learn', 'n1', 1, 1, 1, 'v:A', ['a1', 'a2', 'a3']),
    ev('a4', 'accept', 'n3', 1, 2, 1, 'v:B', []),
    ev('a5', 'accept', 'n4', 1, 2, 1, 'v:B', []),
    ev('a6', 'accept', 'n5', 1, 2, 1, 'v:B', []),
    ev('l2', 'learn', 'n4', 1, 2, 1, 'v:B', ['a4', 'a5', 'a6']),
  ];
}

// A node persists its promise at ballot 5 and then accepts at ballot 3.
export function lowBallotAccept(): TraceEvent[] {
  return [
    ev('p1', 'promise', 'n1', 1, 5, 0, null, []),
    ev('pe1', 'persist', 'n1', 1, 5, 0, null, ['p1']),
    ev('a1', 'accept', 'n1', 1, 3, 1, 'v:A', ['pe1']),
  ];
}

// A node persists promise ballot 5, restarts, and accepts at ballot 3.
export function restartStateLoss(): TraceEvent[] {
  return [
    ev('p1', 'promise', 'n1', 1, 5, 0, null, []),
    ev('pe1', 'persist', 'n1', 1, 5, 0, null, ['p1']),
    ev('r1', 'restart', 'n1', 2, 0, 0, null, ['pe1']),
    ev('a1', 'accept', 'n1', 2, 3, 1, 'v:A', ['r1']),
  ];
}

// Slot 2 is applied before slot 1 on n1, then slot 1 is applied afterwards.
export function outOfOrderApply(): TraceEvent[] {
  return [
    ev('a1', 'accept', 'n1', 1, 1, 1, 'v:A', []),
    ev('a2', 'accept', 'n2', 1, 1, 1, 'v:A', []),
    ev('a3', 'accept', 'n3', 1, 1, 1, 'v:A', []),
    ev('l1', 'learn', 'n1', 1, 1, 1, 'v:A', ['a1', 'a2', 'a3']),
    ev('a4', 'accept', 'n1', 1, 1, 2, 'v:B', []),
    ev('a5', 'accept', 'n2', 1, 1, 2, 'v:B', []),
    ev('a6', 'accept', 'n3', 1, 1, 2, 'v:B', []),
    ev('l2', 'learn', 'n2', 1, 1, 2, 'v:B', ['a4', 'a5', 'a6']),
    ev('ap1', 'apply', 'n1', 1, 0, 2, 'v:B', ['l2']),
    ev('ap2', 'apply', 'n1', 1, 0, 1, 'v:A', ['ap1', 'l1']),
  ];
}

// A late accept and a delayed learn/apply notification: perfectly legal.
export function legalNotificationDelay(): TraceEvent[] {
  return [
    ev('a1', 'accept', 'n1', 1, 1, 1, 'v:A', []),
    ev('a2', 'accept', 'n2', 1, 1, 1, 'v:A', []),
    ev('a3', 'accept', 'n3', 1, 1, 1, 'v:A', []),
    ev('l1', 'learn', 'n1', 1, 1, 1, 'v:A', ['a1', 'a2', 'a3']),
    ev('ap1', 'apply', 'n1', 1, 0, 1, 'v:A', ['l1']),
    ev('a4', 'accept', 'n4', 1, 1, 1, 'v:A', []),
    ev('l2', 'learn', 'n4', 1, 1, 1, 'v:A', ['a1', 'a2', 'a4']),
    ev('ap2', 'apply', 'n4', 1, 0, 1, 'v:A', ['l2']),
  ];
}

// The learn references an accept that never made it into the trace: the
// checker must report incompleteness, not a safety violation.
export function missingLogs(): TraceEvent[] {
  return [
    ev('a1', 'accept', 'n1', 1, 1, 1, 'v:A', []),
    ev('a2', 'accept', 'n2', 1, 1, 1, 'v:A', []),
    ev('l1', 'learn', 'n1', 1, 1, 1, 'v:A', ['a1', 'a2', 'a3']),
  ];
}
