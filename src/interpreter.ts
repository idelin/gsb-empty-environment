/**
 * A small step-driven interpreter supporting yield, try/finally and
 * cooperative cancellation.
 *
 * Semantics:
 * - `cancel()` never interrupts synchronously. The cancellation is injected
 *   at the next resume point (the start of the next `step()` call) as a
 *   `CancelError` thrown at exactly that point.
 * - `finally` blocks may themselves `yield` (cleanup can suspend), but each
 *   finally runs at most once: unwinding never re-enters a finally that is
 *   already running; a re-cancellation abandons it and keeps unwinding.
 * - If a finally throws while an exception/cancellation is in flight, the
 *   cleanup exception overrides the in-flight one, and the original is kept
 *   on the new error's `cause` property as diagnostic information.
 */

export class CancelError extends Error {
  cause?: unknown;

  constructor(message = 'operation cancelled') {
    super(message);
    this.name = 'CancelError';
  }
}

export type StepResult =
  | { type: 'yield'; value: unknown }
  | { type: 'return'; value: unknown }
  | { type: 'throw'; error: unknown };

export type Instr =
  | { op: 'yield'; value: unknown }
  | { op: 'return'; value: unknown }
  | { op: 'throw'; error: unknown }
  | { op: 'pushFinally'; handler: number }
  | { op: 'popFinally' }
  | { op: 'endFinally' };

type Completion =
  | { kind: 'throw'; error: unknown }
  | { kind: 'return'; value: unknown };

interface HandlerFrame {
  handlerPc: number;
  /** 'armed' = try body in progress; 'running' = finally body in progress. */
  state: 'armed' | 'running';
  /** Where to continue when a normally-entered finally finishes. */
  continuation: number;
}

interface HasCause {
  cause?: unknown;
}

/**
 * Attaches `cause` to `error` (unless it already has one). Non-Error thrown
 * values are wrapped so the cause chain is never lost.
 */
function withCause(error: unknown, cause: unknown): unknown {
  if (error instanceof Error) {
    const holder = error as Error & HasCause;
    if (holder.cause === undefined) {
      holder.cause = cause;
    }
    return error;
  }
  const wrapped = new Error(`non-error thrown during cleanup: ${String(error)}`);
  (wrapped as HasCause).cause = cause;
  (wrapped as { thrown?: unknown }).thrown = error;
  return wrapped;
}

export class Interpreter {
  private pc = 0;
  private readonly handlers: HandlerFrame[] = [];
  /** Abrupt completion currently propagating (set while unwinding / in finally). */
  private pending: Completion | null = null;
  private cancelRequested = false;
  private result: StepResult | null = null;

  constructor(private readonly program: Instr[]) {}

  /** Requests cancellation; injected at the next resume point. */
  cancel(): void {
    if (this.result === null) {
      this.cancelRequested = true;
    }
  }

  get done(): boolean {
    return this.result !== null;
  }

  /**
   * Runs until the next yield, or until the program completes.
   * Once finished, returns the final result without changing state.
   */
  step(): StepResult {
    if (this.result !== null) {
      return this.result;
    }
    if (this.cancelRequested) {
      this.cancelRequested = false;
      this.inject({ kind: 'throw', error: new CancelError() });
    }
    while (this.result === null) {
      const instr = this.program[this.pc];
      if (instr === undefined) {
        throw new Error(`program counter out of bounds: ${this.pc}`);
      }
      switch (instr.op) {
        case 'yield':
          this.pc += 1;
          return { type: 'yield', value: instr.value };

        case 'return':
          this.inject({ kind: 'return', value: instr.value });
          break;

        case 'throw': {
          let error: unknown = instr.error;
          if (this.pending !== null && this.pending.kind === 'throw') {
            // A cleanup exception overrides the in-flight one, keeping the
            // original as `cause`.
            error = withCause(error, this.pending.error);
            this.pending = null;
          }
          this.unwind({ kind: 'throw', error });
          break;
        }

        case 'pushFinally':
          this.handlers.push({
            handlerPc: instr.handler,
            state: 'armed',
            continuation: -1,
          });
          this.pc += 1;
          break;

        case 'popFinally': {
          // Normal exit of the try body: run the finally once, then continue.
          const frame = this.handlers.pop();
          if (frame === undefined) {
            throw new Error('popFinally without matching pushFinally');
          }
          frame.state = 'running';
          frame.continuation = this.pc + 1;
          this.handlers.push(frame);
          this.pc = frame.handlerPc;
          break;
        }

        case 'endFinally': {
          const frame = this.handlers.pop();
          if (frame === undefined) {
            throw new Error('endFinally without matching pushFinally');
          }
          if (this.pending !== null) {
            const completion = this.pending;
            this.pending = null;
            this.unwind(completion);
          } else {
            this.pc = frame.continuation;
          }
          break;
        }
      }
    }
    return this.result;
  }

  /**
   * Injects an abrupt completion at the current resume point. If another
   * abrupt completion is already in flight (we are inside a finally), the
   * new one overrides it and keeps the old error as `cause`.
   */
  private inject(completion: Completion): void {
    if (
      completion.kind === 'throw' &&
      this.pending !== null &&
      this.pending.kind === 'throw'
    ) {
      completion = {
        kind: 'throw',
        error: withCause(completion.error, this.pending.error),
      };
      this.pending = null;
    }
    this.unwind(completion);
  }

  /**
   * Unwinds `completion` to the nearest armed finally. Finally frames that
   * are already running are abandoned (each finally executes only once).
   * With no handler left, the completion becomes the final result.
   */
  private unwind(completion: Completion): void {
    while (this.handlers.length > 0) {
      const top = this.handlers[this.handlers.length - 1]!;
      if (top.state === 'armed') {
        top.state = 'running';
        this.pending = completion;
        this.pc = top.handlerPc;
        return;
      }
      this.handlers.pop();
    }
    this.pending = null;
    this.result =
      completion.kind === 'return'
        ? { type: 'return', value: completion.value }
        : { type: 'throw', error: completion.error };
  }
}
