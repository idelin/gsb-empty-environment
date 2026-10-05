// Sample traces for the five required scenarios plus rule-coverage extras.

import type { Ballot, TraceEvent } from '../src/types.ts';

export const MEMBERS = ['n1', 'n2', 'n3', 'n4', 'n5'];

const b = (round: number, node: string): Ballot => ({ round, node });

interface EvSpec {
  id: string;
  kind: TraceEvent['kind'];
  node: string;
  epoch?: number;
  ballot?: Ballot;
  slot?: number;
  value?: string;
  persisted?: TraceEvent['persisted'];
  recovered?: TraceEvent['recovered'];
  causes?: string[];
}

function ev(spec: EvSpec): TraceEvent {
  return { epoch: 1, causes: [], ...spec } as TraceEvent;
}

const accept = (id: string, node: string, slot: number, ballot: Ballot, value: string): TraceEvent =>
  ev({ id, kind: 'accept', node, slot, ballot, value });

/** Legal trace: quorum accepts, learn, in-order applies, a restart that keeps
 *  durable state, and a learn notification that arrives late (which is legal). */
export function legalNotificationDelayTrace(): TraceEvent[] {
  return [
    ev({ id: 'p1', kind: 'promise', node: 'n1', ballot: b(1, 'n1') }),
    ev({ id: 'pe1', kind: 'persist', node: 'n1', persisted: { promised: b(1, 'n1') }, causes: ['p1'] }),
    accept('a1', 'n1', 1, b(1, 'n1'), 'va'),
    ev({
      id: 'pa1', kind: 'persist', node: 'n1',
      persisted: { accepted: [{ slot: 1, ballot: b(1, 'n1'), value: 'va' }] }, causes: ['a1'],
    }),
    accept('a2', 'n2', 1, b(1, 'n1'), 'va'),
    accept('a3', 'n3', 1, b(1, 'n1'), 'va'),
    ev({
      id: 'pa3', kind: 'persist', node: 'n3',
      persisted: { accepted: [{ slot: 1, ballot: b(1, 'n1'), value: 'va' }] }, causes: ['a3'],
    }),
    ev({ id: 'l1', kind: 'learn', node: 'n1', slot: 1, value: 'va', ballot: b(1, 'n1'), causes: ['a1', 'a2', 'a3'] }),
    ev({ id: 'ap1', kind: 'apply', node: 'n1', slot: 1, value: 'va', causes: ['l1'] }),
    // n3 restarts before the learn notification arrives; durable state survives.
    ev({
      id: 'r3', kind: 'restart', node: 'n3',
      recovered: { accepted: [{ slot: 1, ballot: b(1, 'n1'), value: 'va' }] }, causes: ['pa3'],
    }),
    // The notification is delayed but eventually delivered — perfectly legal.
    ev({ id: 'l3', kind: 'learn', node: 'n3', slot: 1, value: 'va', ballot: b(1, 'n1'), causes: ['a1', 'a2', 'a3'] }),
    ev({ id: 'ap3', kind: 'apply', node: 'n3', slot: 1, value: 'va', causes: ['l3'] }),
    // Slot 2 is chosen and applied in order.
    accept('a4', 'n1', 2, b(2, 'n2'), 'vb'),
    accept('a5', 'n2', 2, b(2, 'n2'), 'vb'),
    accept('a6', 'n4', 2, b(2, 'n2'), 'vb'),
    ev({ id: 'l2', kind: 'learn', node: 'n2', slot: 2, value: 'vb', ballot: b(2, 'n2'), causes: ['a4', 'a5', 'a6'] }),
    ev({ id: 'ap1b', kind: 'apply', node: 'n1', slot: 2, value: 'vb', causes: ['l2'] }),
    ev({ id: 'ap3b', kind: 'apply', node: 'n3', slot: 2, value: 'vb', causes: ['l2'] }),
  ];
}

/** Two different values each get a valid quorum for the same slot. */
export function doubleChosenTrace(): TraceEvent[] {
  return [
    accept('a1', 'n1', 1, b(1, 'n1'), 'va'),
    accept('a2', 'n2', 1, b(1, 'n1'), 'va'),
    accept('a3', 'n3', 1, b(1, 'n1'), 'va'),
    ev({ id: 'l1', kind: 'learn', node: 'n1', slot: 1, value: 'va', ballot: b(1, 'n1'), causes: ['a1', 'a2', 'a3'] }),
    accept('a4', 'n3', 1, b(2, 'n5'), 'vb'),
    accept('a5', 'n4', 1, b(2, 'n5'), 'vb'),
    accept('a6', 'n5', 1, b(2, 'n5'), 'vb'),
    ev({ id: 'l2', kind: 'learn', node: 'n5', slot: 1, value: 'vb', ballot: b(2, 'n5'), causes: ['a4', 'a5', 'a6'] }),
  ];
}

/** Same as doubleChosen but with a fourth accept for "va": exercises the
 *  lexicographic tie-break between equal-size minimal evidence sets. */
export function doubleChosenTieBreakTrace(): TraceEvent[] {
  return [
    accept('q1', 'n1', 1, b(1, 'n1'), 'va'),
    accept('q2', 'n2', 1, b(1, 'n1'), 'va'),
    accept('q3', 'n3', 1, b(1, 'n1'), 'va'),
    accept('q4', 'n4', 1, b(1, 'n1'), 'va'),
    ev({ id: 'l1', kind: 'learn', node: 'n1', slot: 1, value: 'va', ballot: b(1, 'n1'), causes: ['q1', 'q2', 'q3', 'q4'] }),
    accept('r1', 'n3', 1, b(2, 'n5'), 'vb'),
    accept('r2', 'n4', 1, b(2, 'n5'), 'vb'),
    accept('r3', 'n5', 1, b(2, 'n5'), 'vb'),
    ev({ id: 'l2', kind: 'learn', node: 'n5', slot: 1, value: 'vb', ballot: b(2, 'n5'), causes: ['r1', 'r2', 'r3'] }),
  ];
}

/** An accept below the node's persisted promise. */
export function lowBallotAcceptTrace(): TraceEvent[] {
  return [
    ev({ id: 'p1', kind: 'promise', node: 'n1', ballot: b(5, 'n1') }),
    ev({ id: 'pe1', kind: 'persist', node: 'n1', persisted: { promised: b(5, 'n1') }, causes: ['p1'] }),
    accept('a1', 'n1', 1, b(2, 'n1'), 'vx'),
  ];
}

/** A restart that forgets confirmed durable state. */
export function restartLostStateTrace(): TraceEvent[] {
  return [
    accept('a1', 'n1', 1, b(2, 'n1'), 'va'),
    ev({
      id: 'pe1', kind: 'persist', node: 'n1',
      persisted: { promised: b(3, 'n1'), accepted: [{ slot: 1, ballot: b(2, 'n1'), value: 'va' }] },
      causes: ['a1'],
    }),
    ev({ id: 'r1', kind: 'restart', node: 'n1', recovered: { promised: b(1, 'n1'), accepted: [] }, causes: ['pe1'] }),
  ];
}

/** Slot 2 is applied before slot 1. */
export function outOfOrderApplyTrace(): TraceEvent[] {
  return [
    accept('a1', 'n1', 1, b(1, 'n1'), 'va'),
    accept('a2', 'n2', 1, b(1, 'n1'), 'va'),
    accept('a3', 'n3', 1, b(1, 'n1'), 'va'),
    ev({ id: 'l1', kind: 'learn', node: 'n1', slot: 1, value: 'va', ballot: b(1, 'n1'), causes: ['a1', 'a2', 'a3'] }),
    accept('a4', 'n1', 2, b(1, 'n1'), 'vb'),
    accept('a5', 'n2', 2, b(1, 'n1'), 'vb'),
    accept('a6', 'n3', 2, b(1, 'n1'), 'vb'),
    ev({ id: 'l2', kind: 'learn', node: 'n2', slot: 2, value: 'vb', ballot: b(1, 'n1'), causes: ['a4', 'a5', 'a6'] }),
    ev({ id: 'ap1', kind: 'apply', node: 'n1', slot: 2, value: 'vb', causes: ['l2'] }),
  ];
}

/** A learn whose referenced accepts never reach a quorum. */
export function unquorumedLearnTrace(): TraceEvent[] {
  return [
    accept('a1', 'n1', 1, b(1, 'n1'), 'va'),
    accept('a2', 'n2', 1, b(1, 'n1'), 'va'),
    ev({ id: 'l1', kind: 'learn', node: 'n1', slot: 1, value: 'va', ballot: b(1, 'n1'), causes: ['a1', 'a2'] }),
  ];
}

/** An apply whose learn reference carries a different value. */
export function applyValueMismatchTrace(): TraceEvent[] {
  return [
    accept('a1', 'n1', 1, b(1, 'n1'), 'va'),
    accept('a2', 'n2', 1, b(1, 'n1'), 'va'),
    accept('a3', 'n3', 1, b(1, 'n1'), 'va'),
    ev({ id: 'l1', kind: 'learn', node: 'n1', slot: 1, value: 'va', ballot: b(1, 'n1'), causes: ['a1', 'a2', 'a3'] }),
    ev({ id: 'ap1', kind: 'apply', node: 'n1', slot: 1, value: 'vb', causes: ['l1'] }),
  ];
}

/** An apply justified by a learn that itself has no quorum. */
export function applyNotChosenTrace(): TraceEvent[] {
  return [
    accept('a1', 'n1', 1, b(1, 'n1'), 'va'),
    accept('a2', 'n2', 1, b(1, 'n1'), 'va'),
    ev({ id: 'l1', kind: 'learn', node: 'n1', slot: 1, value: 'va', ballot: b(1, 'n1'), causes: ['a1', 'a2'] }),
    ev({ id: 'ap1', kind: 'apply', node: 'n1', slot: 1, value: 'va', causes: ['l1'] }),
  ];
}

/** Missing causal events: the checker must report incomplete, never violations. */
export function missingLogsTrace(): TraceEvent[] {
  return [
    accept('a1', 'n1', 1, b(1, 'n1'), 'va'),
    accept('a2', 'n2', 1, b(1, 'n1'), 'va'),
    // references 'a3', which is absent from the trace
    ev({ id: 'l1', kind: 'learn', node: 'n1', slot: 1, value: 'va', ballot: b(1, 'n1'), causes: ['a1', 'a2', 'a3'] }),
    // references 'l9', which is absent from the trace
    ev({ id: 'ap1', kind: 'apply', node: 'n2', slot: 1, value: 'va', causes: ['l9'] }),
  ];
}
