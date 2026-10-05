import { Journal } from './journal.ts';
import type { JournalRecord } from './journal.ts';
import { ReceiptLostError } from './adapters.ts';

export type TerminalState = 'completed' | 'failed' | 'cancelled';
export type FlowStatus = TerminalState | 'running' | 'compensating';

export interface FlowStep {
  name: string;
  /** Perform the external action. Must be idempotent on `key`. */
  run: (key: string) => unknown;
  /** Undo the action. Must be idempotent on `key`. */
  compensate: (key: string) => unknown;
  /** Idempotency-key lookup used during recovery to resolve unknown outcomes. */
  query: (key: string) => unknown | null;
}

export interface FlowDef {
  flowId: string;
  steps: FlowStep[];
}

export function actionKey(flowId: string, step: string): string {
  return `flow:${flowId}:step:${step}`;
}

export function compensationKey(flowId: string, step: string): string {
  return `flow:${flowId}:step:${step}:comp`;
}

interface StepJournal {
  intent?: JournalRecord;
  result?: JournalRecord;
  compIntent?: JournalRecord;
  compResult?: JournalRecord;
}

/**
 * Persistent saga executor.
 *
 * Logging discipline (every record is fsync-durable before the executor acts
 * on it — see Journal.append):
 *  - action_intent is logged BEFORE the adapter call;
 *  - action_result is logged AFTER the adapter call, with status
 *    ok | failed | unknown (unknown = receipt lost, outcome ambiguous);
 *  - compensation_intent / compensation_result mirror the above while
 *    compensating in reverse order.
 *
 * Recovery never treats an unknown outcome as a failure: it first reconciles
 * via query(idempotencyKey) against the adapter, and only then records the
 * resolved result.
 *
 * Compensation runs strictly for steps confirmed ok, in reverse order. A
 * transient compensation failure leaves the flow in 'compensating' (no
 * terminal record), so a later resume() can finish it. Steps whose
 * compensation already completed are skipped and never re-applied.
 *
 * Terminal states are single-assignment: appendTerminal re-checks the journal
 * and backs off if a terminal record already exists, so a cancel racing with
 * final success yields exactly one terminal state.
 */
export class Executor {
  readonly journal: Journal;
  private afterAppend?: (rec: JournalRecord) => void;

  constructor(journal: Journal, afterAppend?: (rec: JournalRecord) => void) {
    this.journal = journal;
    this.afterAppend = afterAppend;
  }

  private log(type: string, payload: Record<string, unknown>): JournalRecord {
    const rec = this.journal.append(type, payload);
    this.afterAppend?.(rec);
    return rec;
  }

  private stepState(stepName: string): StepJournal {
    const st: StepJournal = {};
    for (const rec of this.journal.records) {
      if (rec.step !== stepName) continue;
      if (rec.type === 'action_intent') st.intent = rec;
      else if (rec.type === 'action_result') st.result = rec;
      else if (rec.type === 'compensation_intent') st.compIntent = rec;
      else if (rec.type === 'compensation_result') st.compResult = rec;
    }
    return st;
  }

  private terminalState(): TerminalState | null {
    for (const rec of this.journal.records) {
      if (rec.type === 'terminal') return rec.state as TerminalState;
    }
    return null;
  }

  private isCancelRequested(): boolean {
    return this.journal.records.some((r) => r.type === 'cancel_requested');
  }

  status(): FlowStatus {
    const terminal = this.terminalState();
    if (terminal) return terminal;
    const compensating = this.journal.records.some(
      (r) =>
        r.type === 'compensation_result' &&
        (r.status === 'failed' || r.status === 'unknown'),
    );
    return compensating ? 'compensating' : 'running';
  }

  /** Start a flow from the beginning. */
  run(flow: FlowDef): FlowStatus {
    if (!this.journal.records.some((r) => r.type === 'flow_start')) {
      this.log('flow_start', {
        flow: flow.flowId,
        steps: flow.steps.map((s) => s.name),
      });
    }
    return this.execute(flow);
  }

  /** Resume a flow after a crash/restart using the existing journal. */
  resume(flow: FlowDef): FlowStatus {
    if (!this.journal.records.some((r) => r.type === 'flow_start')) {
      throw new Error(`cannot resume flow ${flow.flowId}: no flow_start record`);
    }
    return this.execute(flow);
  }

  /**
   * Request cancellation. If a terminal state already exists the cancel is a
   * no-op (duplicate cancels included) and the existing terminal state wins.
   */
  cancel(flowId: string): TerminalState | 'cancel_requested' {
    const existing = this.terminalState();
    if (existing) return existing;
    if (!this.isCancelRequested()) {
      this.log('cancel_requested', { flow: flowId });
    }
    return 'cancel_requested';
  }

  private appendTerminal(flow: FlowDef, state: TerminalState): TerminalState {
    const existing = this.terminalState();
    if (existing) return existing; // lost the race: exactly one terminal state
    this.log('terminal', { flow: flow.flowId, state });
    return state;
  }

  private execute(flow: FlowDef): FlowStatus {
    const existing = this.terminalState();
    if (existing) return existing;

    let failure = false;

    for (const step of flow.steps) {
      const st = this.stepState(step.name);
      if (st.result?.status === 'ok') continue;
      if (st.result?.status === 'failed') {
        failure = true;
        break;
      }
      if (st.intent) {
        // Unknown outcome (intent logged, result missing or 'unknown'):
        // reconcile via the idempotency key BEFORE deciding anything.
        const key = st.intent.key as string;
        const receipt = step.query(key);
        if (receipt !== null && receipt !== undefined) {
          this.log('action_result', {
            step: step.name,
            key,
            status: 'ok',
            via: 'recovery-query',
            receipt,
          });
          continue;
        }
        // Query proves the action never executed, so re-running it with the
        // same idempotency key is safe. Fall through to execution below
        // (the intent record already exists and is not duplicated).
      }
      if (this.isCancelRequested()) break; // cancel takes effect between steps

      const key = actionKey(flow.flowId, step.name);
      if (!st.intent) {
        this.log('action_intent', { step: step.name, key });
      }
      let receipt: unknown;
      try {
        receipt = step.run(key);
      } catch (err) {
        if (err instanceof ReceiptLostError) {
          this.log('action_result', {
            step: step.name,
            key,
            status: 'unknown',
            error: String(err),
          });
          return 'running'; // suspended; recovery must reconcile by key
        }
        this.log('action_result', {
          step: step.name,
          key,
          status: 'failed',
          error: String(err),
        });
        failure = true;
        break;
      }
      this.log('action_result', { step: step.name, key, status: 'ok', receipt });
    }

    const allOk = flow.steps.every(
      (s) => this.stepState(s.name).result?.status === 'ok',
    );
    if (!failure && !this.isCancelRequested() && allOk) {
      return this.appendTerminal(flow, 'completed');
    }

    // Compensate confirmed-successful steps only, in reverse order.
    const succeeded = flow.steps
      .filter((s) => this.stepState(s.name).result?.status === 'ok')
      .reverse();
    for (const step of succeeded) {
      const st = this.stepState(step.name);
      if (st.compResult?.status === 'ok') continue; // already compensated: skip
      const compKey = compensationKey(flow.flowId, step.name);
      if (st.compIntent && (!st.compResult || st.compResult.status === 'unknown')) {
        const receipt = step.query(compKey);
        if (receipt !== null && receipt !== undefined) {
          this.log('compensation_result', {
            step: step.name,
            key: compKey,
            status: 'ok',
            via: 'recovery-query',
            receipt,
          });
          continue;
        }
      }
      if (!st.compIntent) {
        this.log('compensation_intent', { step: step.name, key: compKey });
      }
      let receipt: unknown;
      try {
        receipt = step.compensate(compKey);
      } catch (err) {
        const status = err instanceof ReceiptLostError ? 'unknown' : 'failed';
        this.log('compensation_result', {
          step: step.name,
          key: compKey,
          status,
          error: String(err),
        });
        return 'compensating'; // transient: resumable, no terminal record yet
      }
      this.log('compensation_result', {
        step: step.name,
        key: compKey,
        status: 'ok',
        receipt,
      });
    }

    return this.appendTerminal(flow, this.isCancelRequested() ? 'cancelled' : 'failed');
  }
}
