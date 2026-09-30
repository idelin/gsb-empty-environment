// Independent invariant checker. It does NOT look at arbiter internals;
// it only consumes the input trace plus the recorded (state, output) log
// and verifies legal state transitions and commit/compensation accounting.

import type { ArbiterState } from './arbiter.ts';
import type { GestureEvent } from './types.ts';
import type { TraceOp } from './traceGen.ts';

export interface StepRecord {
  stateBefore: ArbiterState;
  stateAfter: ArbiterState;
  outputs: GestureEvent[];
}

export interface CheckFailure {
  trace: string;
  step: number;
  rule: string;
  detail: string;
}

const LEGAL_TRANSITIONS: Record<ArbiterState, ArbiterState[]> = {
  idle: ['idle', 'tracking'],
  tracking: ['idle', 'tracking', 'drag-preview', 'pinch-preview'],
  'drag-preview': ['idle', 'tracking', 'drag-preview'],
  'pinch-preview': ['idle', 'tracking', 'pinch-preview'],
};

const FINAL_KINDS = new Set(['drag', 'pinch']);

export function checkTrace(name: string, ops: TraceOp[], steps: StepRecord[]): CheckFailure[] {
  const failures: CheckFailure[] = [];
  const fail = (step: number, rule: string, detail: string) =>
    failures.push({ trace: name, step, rule, detail });

  // Recompute the active pointer set independently from the input trace.
  const active = new Set<number>();
  // Per interaction: sequence of gesture events for accounting.
  const perInteraction = new Map<number, GestureEvent[]>();

  for (let i = 0; i < steps.length; i++) {
    const step = steps[i]!;
    const op = ops[i]!;

    // 1. Legal state transitions.
    const allowed = LEGAL_TRANSITIONS[step.stateBefore];
    if (!allowed.includes(step.stateAfter)) {
      fail(i, 'state-transition', `${step.stateBefore} -> ${step.stateAfter} is not legal`);
    }

    // 2. Bookkeeping of the active pointer set from inputs.
    if (op.kind === 'event') {
      const ev = op.event;
      if (ev.type === 'pointerdown') active.add(ev.pointerId);
      else if (ev.type === 'pointerup' || ev.type === 'pointercancel' || ev.type === 'lostpointercapture') {
        active.delete(ev.pointerId);
      }
    }

    // 3. Outputs must only reference currently-active pointers (a commit may
    //    include the pointer being removed by this very event).
    for (const out of step.outputs) {
      for (const pid of out.pointerIds) {
        const departing = op.kind === 'event' && op.event.pointerId === pid &&
          (op.event.type === 'pointerup' || op.event.type === 'pointercancel' ||
           op.event.type === 'lostpointercapture');
        if (!active.has(pid) && !departing) {
          fail(i, 'pointer-set', `event for inactive pointer ${pid}: ${out.kind}/${out.phase}`);
        }
      }
      const list = perInteraction.get(out.interactionId) ?? [];
      list.push(out);
      perInteraction.set(out.interactionId, list);
    }
  }

  // 4. Per-interaction accounting.
  for (const [iid, events] of perInteraction) {
    const finalCommits = events.filter((e) => e.phase === 'commit' && FINAL_KINDS.has(e.kind));
    if (finalCommits.length > 1) {
      fail(-1, 'exclusive-commit',
        `interaction ${iid}: ${finalCommits.length} final commits (${finalCommits.map((e) => e.kind).join(',')})`);
    }
    const clickCommits = events.filter((e) => e.kind === 'click' && e.phase === 'commit');
    if (clickCommits.length > 1) {
      fail(-1, 'click-count', `interaction ${iid}: ${clickCommits.length} click commits`);
    }
    const anyPreview = events.some((e) => e.phase === 'preview');
    if (clickCommits.length > 0 && anyPreview) {
      fail(-1, 'click-after-preview', `interaction ${iid}: click committed although a preview was emitted`);
    }
    // 5. Every preview is closed by exactly one commit or compensate.
    const open = new Map<string, number>(); // kind+generation -> open preview count
    for (const e of events) {
      if (e.kind === 'click') continue;
      const key = `${e.kind}#${e.generation}`;
      if (e.phase === 'preview') {
        open.set(key, (open.get(key) ?? 0) + 1);
      } else if (e.phase === 'commit' || e.phase === 'compensate') {
        if (!open.has(key) || open.get(key) === 0) {
          fail(-1, 'unopened-close', `interaction ${iid}: ${e.phase} without preview for ${key}`);
        } else {
          open.delete(key);
        }
      }
    }
    for (const key of open.keys()) {
      fail(-1, 'unclosed-preview', `interaction ${iid}: preview ${key} never committed nor compensated`);
    }
  }

  // 6. Termination: last step must end idle (no pointer stays active forever).
  const last = steps[steps.length - 1];
  if (last && last.stateAfter !== 'idle') {
    fail(steps.length - 1, 'termination', `final state is ${last.stateAfter}, expected idle`);
  }

  return failures;
}
