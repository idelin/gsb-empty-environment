/**
 * 小型解释器：支持 yield、try/finally 与取消。
 *
 * 语义约定：
 * - 取消通过 cancel() 请求，只在“恢复点”（上一次 yield 挂起处）注入，
 *   表现为在该点抛出 CancelledError。
 * - try 的 finally 在每条退出路径（正常结束 / return / 异常 / 取消）上
 *   恰好执行一次；finally 内部可以继续 yield（解释器随之挂起）。
 * - 若 finally 自身抛出（清理异常，含再次取消），清理异常覆盖原异常，
 *   原异常作为 cause 保留在清理异常上。
 */

export type Instr =
  | { kind: "yield"; value?: unknown }
  | { kind: "do"; effect: () => void }
  | { kind: "try"; body: Instr[]; finally: Instr[] }
  | { kind: "return"; value?: unknown };

export type StepResult =
  | { type: "yield"; value: unknown }
  | { type: "return"; value: unknown };

export class CancelledError extends Error {
  constructor(message = "cancelled", options?: { cause?: unknown }) {
    super(message, options);
    this.name = "CancelledError";
  }
}

/** finally 抛出的值不是 Error（或已带 cause）时，用它包装并关联原异常。 */
export class CleanupError extends Error {
  readonly cleanup: unknown;
  constructor(cleanup: unknown, original: unknown) {
    super("cleanup failed while handling another exception", { cause: original });
    this.name = "CleanupError";
    this.cleanup = cleanup;
  }
}

type Completion = { type: "normal" } | { type: "return"; value: unknown };
const NORMAL: Completion = { type: "normal" };

/** 清理异常覆盖原异常，原异常保留为 cause。 */
function overrideWithCause(cleanupErr: unknown, original: unknown): unknown {
  if (cleanupErr instanceof Error && cleanupErr.cause === undefined) {
    (cleanupErr as { cause?: unknown }).cause = original;
    return cleanupErr;
  }
  return new CleanupError(cleanupErr, original);
}

function* exec(instrs: Instr[]): Generator<unknown, Completion, unknown> {
  for (const instr of instrs) {
    switch (instr.kind) {
      case "yield":
        yield instr.value;
        break;
      case "do":
        instr.effect();
        break;
      case "return":
        return { type: "return", value: instr.value };
      case "try": {
        let bodyCompletion: Completion = NORMAL;
        let pending: unknown;
        let hasPending = false;
        try {
          bodyCompletion = yield* exec(instr.body);
        } catch (err) {
          hasPending = true;
          pending = err;
        }
        // finally 恰好执行一次：正常、return、异常、取消都汇聚到这里。
        try {
          const finallyCompletion = yield* exec(instr.finally);
          if (finallyCompletion.type !== "normal") return finallyCompletion;
        } catch (cleanupErr) {
          throw hasPending ? overrideWithCause(cleanupErr, pending) : cleanupErr;
        }
        if (hasPending) throw pending;
        if (bodyCompletion.type !== "normal") return bodyCompletion;
        break;
      }
    }
  }
  return NORMAL;
}

export class Interpreter {
  private readonly gen: Generator<unknown, Completion, unknown>;
  private suspended = false;
  private finished = false;
  private cancelRequested = false;

  constructor(program: Instr[]) {
    this.gen = exec(program);
  }

  get done(): boolean {
    return this.finished;
  }

  get cancelPending(): boolean {
    return this.cancelRequested;
  }

  /** 请求取消；在下一次从 yield 恢复时注入 CancelledError。 */
  cancel(): void {
    this.cancelRequested = true;
  }

  /** 运行到下一个 yield / 结束；未捕获的异常（含取消）从本方法抛出。 */
  step(): StepResult {
    if (this.finished) throw new Error("interpreter already finished");
    let result: IteratorResult<unknown, Completion>;
    try {
      if (this.cancelRequested && this.suspended) {
        this.cancelRequested = false;
        this.suspended = false;
        result = this.gen.throw(new CancelledError());
      } else {
        result = this.gen.next();
      }
    } catch (err) {
      this.finished = true;
      this.suspended = false;
      throw err;
    }
    if (result.done) {
      this.finished = true;
      this.suspended = false;
      this.cancelRequested = false;
      const completion = result.value;
      return {
        type: "return",
        value: completion.type === "return" ? completion.value : undefined,
      };
    }
    this.suspended = true;
    return { type: "yield", value: result.value };
  }
}
