import type {
  TraceEvent,
  Claim,
  ViolationKind,
  IncompleteNote,
  ConflictRecord,
  CheckReport,
  CheckOptions,
  Violation,
} from './types';
import {
  MEMBERS,
  BALLOT_KINDS,
  isValidBallot,
  validateEvent,
  normalizeEvent,
  canonical,
} from './model';
import { indexById, causalPast, missingInPast } from './graph';
import { minimizeEvidence } from './minimize';

const DEFAULT_BUDGET: number = 50000;

interface RawViolation {
  claim: Claim;
  detail: string;
  closure: string[];
}

export interface IngestResult {
  events: TraceEvent[];
  duplicates: number;
  conflicts: ConflictRecord[];
  errors: string[];
}

// Ingests raw events: validates shape, applies duplicate-id idempotence
// (identical content is dropped) and rejects conflicting duplicates.
export function ingest(rawEvents: unknown[]): IngestResult {
  const errors: string[] = [];
  const events: TraceEvent[] = [];
  const seen: Map<string, TraceEvent> = new Map();
  const conflicts: ConflictRecord[] = [];
  let duplicates = 0;
  for (let i = 0; i < rawEvents.length; i++) {
    const problems = validateEvent(rawEvents[i]);
    if (problems.length > 0) {
      errors.push('event[' + i + ']: ' + problems.join('; '));
      continue;
    }
    const e = normalizeEvent(rawEvents[i]);
    const prev = seen.get(e.id);
    if (prev) {
      if (canonical(prev) === canonical(e)) {
        duplicates++;
        continue;
      }
      conflicts.push({ id: e.id, first: prev, second: e });
      continue;
    }
    seen.set(e.id, e);
    events.push(e);
  }
  return { events, duplicates, conflicts, errors };
}

export interface FindResult {
  violations: RawViolation[];
  incomplete: IncompleteNote[];
}

// Evaluates the safety rules over the deduplicated trace. Existence-based
// violations fire as soon as their witness events are present; absence-based
// checks (quorum, chosen-before-apply, apply predecessor) require a fully
// resolved causal past, otherwise they are reported as incomplete instead of
// being treated as implementation faults.
export function findViolations(events: TraceEvent[], quorum: number): FindResult {
  const index = indexById(events);
  const violations: RawViolation[] = [];
  const incomplete: IncompleteNote[] = [];
  const violationKeys: Set<string> = new Set();
  const noteKeys: Set<string> = new Set();

  const pastCache: Map<string, Set<string>> = new Map();
  const missingCache: Map<string, Set<string>> = new Map();
  const pastOf = (id: string): Set<string> => {
    let s = pastCache.get(id);
    if (!s) {
      s = causalPast([id], index);
      pastCache.set(id, s);
    }
    return s;
  };
  const missingOf = (id: string): Set<string> => {
    let s = missingCache.get(id);
    if (!s) {
      s = missingInPast([id], index);
      missingCache.set(id, s);
    }
    return s;
  };
  const isClosed = (id: string): boolean => missingOf(id).size === 0;

  const note = (eventId: string, reason: string): void => {
    const key = eventId + '|' + reason;
    if (noteKeys.has(key)) return;
    noteKeys.add(key);
    incomplete.push({ eventId, missing: Array.from(missingOf(eventId)).sort(), reason });
  };

  const push = (
    kind: ViolationKind,
    focus: string[],
    slot: number,
    node: string | null,
    value: string | null,
    detail: string
  ): void => {
    const key = kind + '|' + focus.slice().sort().join(',');
    if (violationKeys.has(key)) return;
    violationKeys.add(key);
    const claim: Claim = { kind, focus: focus.slice().sort(), slot, node, value, detail };
    const closure = Array.from(causalPast(focus, index)).sort();
    violations.push({ claim, detail, closure });
  };

  for (const e of events) {
    if (missingOf(e.id).size > 0) {
      note(e.id, 'causal reference(s) not present in trace');
    }
  }

  for (const e of events) {
    if (BALLOT_KINDS.indexOf(e.kind) >= 0 && !isValidBallot(e.ballot)) {
      push('invalid_ballot', [e.id], e.slot, e.node, e.value,
        'event ' + e.id + ' carries ballot ' + e.ballot + ' outside the total order');
    }
  }

  const badAccepts: Set<string> = new Set();

  const restartBetween = (e: TraceEvent, p: TraceEvent): TraceEvent | null => {
    let best: TraceEvent | null = null;
    for (const rid of pastOf(e.id)) {
      const r = index.get(rid);
      if (!r || r.kind !== 'restart' || r.node !== e.node) continue;
      if (!pastOf(r.id).has(p.id)) continue;
      if (!best || r.id < best.id) best = r;
    }
    return best;
  };

  for (const e of events) {
    if (e.kind !== 'accept' && e.kind !== 'promise') continue;
    if (!isValidBallot(e.ballot)) continue;
    const past = pastOf(e.id);
    let worst: TraceEvent | null = null;
    for (const pid of past) {
      const p = index.get(pid);
      if (!p || p.kind !== 'persist' || p.node !== e.node) continue;
      if (!isValidBallot(p.ballot)) continue;
      if (p.ballot > e.ballot) {
        if (!worst || p.ballot > worst.ballot || (p.ballot === worst.ballot && p.id < worst.id)) worst = p;
      }
    }
    if (worst) {
      const r = restartBetween(e, worst);
      if (r) {
        push('restart_state_loss', [e.id, r.id, worst.id], e.slot, e.node, e.value,
          e.kind + ' ' + e.id + ' at ballot ' + e.ballot + ' after restart ' + r.id +
          ' lost persisted promise ballot ' + worst.ballot);
      } else if (e.kind === 'accept') {
        push('accept_below_promise', [e.id, worst.id], e.slot, e.node, e.value,
          'accept ' + e.id + ' at ballot ' + e.ballot + ' is below persisted promise ballot ' + worst.ballot);
      }
      if (e.kind === 'accept') badAccepts.add(e.id);
    }
    if (e.kind === 'accept') {
      let done = false;
      for (const rid of past) {
        if (done) break;
        const r = index.get(rid);
        if (!r || r.kind !== 'restart' || r.node !== e.node) continue;
        for (const pid of pastOf(r.id)) {
          const p = index.get(pid);
          if (!p || p.kind !== 'persist' || p.node !== e.node) continue;
          if (p.value !== null && p.slot >= 1 && p.slot === e.slot && p.value !== e.value) {
            push('restart_state_loss', [e.id, r.id, p.id], e.slot, e.node, e.value,
              'accept ' + e.id + ' after restart ' + r.id + ' overwrote persisted value ' +
              p.value + ' at slot ' + p.slot + ' with ' + e.value);
            badAccepts.add(e.id);
            done = true;
            break;
          }
        }
      }
    }
  }

  const justifiedLearns: TraceEvent[] = [];
  for (const e of events) {
    if (e.kind !== 'learn') continue;
    if (!isValidBallot(e.ballot)) continue;
    const past = pastOf(e.id);
    const nodes: Set<string> = new Set();
    for (const pid of past) {
      const a = index.get(pid);
      if (!a || a.kind !== 'accept') continue;
      if (badAccepts.has(a.id)) continue;
      if (!isValidBallot(a.ballot)) continue;
      if (a.slot === e.slot && a.value === e.value && a.ballot <= e.ballot) nodes.add(a.node);
    }
    if (nodes.size >= quorum) {
      justifiedLearns.push(e);
      continue;
    }
    if (isClosed(e.id)) {
      push('learn_without_quorum', [e.id], e.slot, e.node, e.value,
        'learn ' + e.id + ' chose ' + e.value + ' at slot ' + e.slot + ' with only ' +
        nodes.size + ' distinct valid acceptor(s), quorum is ' + quorum);
    } else {
      note(e.id, 'cannot verify learn quorum: causal log incomplete');
    }
  }

  const learnsBySlot: Map<number, TraceEvent[]> = new Map();
  for (const l of justifiedLearns) {
    const list = learnsBySlot.get(l.slot) || [];
    list.push(l);
    learnsBySlot.set(l.slot, list);
  }
  for (const slot of Array.from(learnsBySlot.keys()).sort((x: number, y: number) => x - y)) {
    const learns = learnsBySlot.get(slot) || [];
    const byValue: Map<string, string> = new Map();
    for (const l of learns) {
      if (l.value === null) continue;
      const cur = byValue.get(l.value);
      if (!cur || l.id < cur) byValue.set(l.value, l.id);
    }
    const reps = Array.from(byValue.values()).sort();
    if (reps.length >= 2) {
      push('conflicting_chosen', [reps[0], reps[1]], slot, null, null,
        'slot ' + slot + ' chosen with conflicting values by learns ' + reps[0] + ' and ' + reps[1]);
    }
  }

  for (const e of events) {
    if (e.kind !== 'apply') continue;
    const past = pastOf(e.id);
    let offender: TraceEvent | null = null;
    for (const pid of past) {
      const pa = index.get(pid);
      if (!pa || pa.kind !== 'apply' || pa.node !== e.node || pa.id === e.id) continue;
      if (pa.slot >= e.slot) {
        if (!offender || pa.slot > offender.slot || (pa.slot === offender.slot && pa.id < offender.id)) {
          offender = pa;
        }
      }
    }
    if (offender) {
      push('apply_out_of_order', [offender.id, e.id], e.slot, e.node, e.value,
        'apply ' + e.id + ' at slot ' + e.slot + ' follows apply ' + offender.id +
        ' at slot ' + offender.slot + ' on ' + e.node);
    } else if (e.slot > 1) {
      let hasPrev = false;
      for (const pid of past) {
        const pa = index.get(pid);
        if (pa && pa.kind === 'apply' && pa.node === e.node && pa.slot === e.slot - 1) {
          hasPrev = true;
          break;
        }
      }
      if (!hasPrev) {
        if (isClosed(e.id)) {
          push('apply_out_of_order', [e.id], e.slot, e.node, e.value,
            'apply ' + e.id + ' at slot ' + e.slot + ' on ' + e.node +
            ' skips slot ' + (e.slot - 1) + ' (slots must advance consecutively from 1)');
        } else {
          note(e.id, 'cannot verify apply order: causal log incomplete');
        }
      }
    }
    let chosen = false;
    for (const pid of past) {
      const l = index.get(pid);
      if (l && l.kind === 'learn' && l.slot === e.slot && l.value === e.value) {
        chosen = true;
        break;
      }
    }
    if (!chosen) {
      if (isClosed(e.id)) {
        push('apply_without_chosen', [e.id], e.slot, e.node, e.value,
          'apply ' + e.id + ' at slot ' + e.slot + ' has no learn of ' + e.value + ' in its causal past');
      } else {
        note(e.id, 'cannot verify chosen-before-apply: causal log incomplete');
      }
    }
  }

  return { violations, incomplete };
}

export function checkTrace(rawEvents: unknown, options?: Partial<CheckOptions>): CheckReport {
  const budget = options && typeof options.budget === 'number' ? options.budget : DEFAULT_BUDGET;
  const members = options && options.members ? options.members : MEMBERS;
  const quorum = Math.floor(members.length / 2) + 1;
  const report: CheckReport = {
    status: 'ok',
    members: members.slice(),
    quorum,
    eventCount: 0,
    duplicateCount: 0,
    errors: [],
    conflicts: [],
    violations: [],
    incomplete: [],
  };
  if (!Array.isArray(rawEvents)) {
    report.errors.push('trace must be an array of events');
    report.status = 'rejected';
    return report;
  }
  const ing = ingest(rawEvents);
  report.errors = ing.errors;
  report.duplicateCount = ing.duplicates;
  report.conflicts = ing.conflicts;
  report.eventCount = ing.events.length;
  for (const e of ing.events) {
    if (members.indexOf(e.node) < 0) {
      report.errors.push('event ' + e.id + ': node ' + e.node + ' is not a fixed member');
    }
  }
  if (report.errors.length > 0 || report.conflicts.length > 0) {
    report.status = 'rejected';
    return report;
  }
  const found = findViolations(ing.events, quorum);
  report.incomplete = found.incomplete;
  for (const v of found.violations) {
    const evidence = minimizeEvidence(ing.events, v.closure, v.claim, budget);
    const violation: Violation = {
      kind: v.claim.kind,
      claim: v.claim,
      detail: v.detail,
      closure: v.closure,
      evidence,
    };
    report.violations.push(violation);
  }
  if (report.violations.length > 0) report.status = 'violations';
  else if (report.incomplete.length > 0) report.status = 'incomplete';
  return report;
}
