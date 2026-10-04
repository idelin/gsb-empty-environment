/**
 * 分阶段作业执行器：准备(prepare) -> 提交(submit) -> 清理(cleanup)。
 *
 * 核心不变量：
 * 1. 准备期可以取消；一旦提交开始，取消只能被记录，执行器必须等待提交驱动
 *    给出明确结果(accepted / rejected)。
 * 2. 清理失败不得覆盖提交状态；提交结果在恢复记录中优先保留。
 * 3. 对外状态(publicStatusOf)在 submission.started 之后永远不会把作业
 *    报告为"未执行"——状态枚举里根本不存在 "not-executed"，
 *    提交已开始但结果未知时只能报告 "submit-outcome-unknown"。
 */

export type Phase =
  | 'idle'
  | 'preparing'
  | 'submitting'
  | 'cleaning'
  | 'done'
  | 'cancelled'
  | 'failed';

export interface SubmissionAccepted {
  kind: 'accepted';
  ref: string;
}

export interface SubmissionRejected {
  kind: 'rejected';
  reason: string;
}

/** 提交阶段的明确结果：提交驱动必须以此二选一落定，不允许无声失败。 */
export type SubmissionOutcome = SubmissionAccepted | SubmissionRejected;

/** 可 JSON 序列化的恢复记录。每次状态迁移后都会持久化快照。 */
export interface RecoveryRecord {
  version: 1;
  jobId: string;
  phase: Phase;
  submission: {
    /** 提交是否已经开始（写前日志：调用 submit 之前先置 true 并持久化）。 */
    started: boolean;
    /** 幂等/查询令牌，恢复时用它向提交方查询明确结果。 */
    token: string | null;
    /** 明确结果；为 null 且 started=true 时表示结果未知，必须恢复查询。 */
    outcome: SubmissionOutcome | null;
  };
  cleanup: {
    attempts: number;
    lastError: string | null;
  };
  cancelRequested: boolean;
  error: string | null;
  updatedAt: number;
}

/**
 * 对外状态。注意：枚举中刻意没有 "not-executed"。
 * 提交一旦开始，对外只能报告 submitted / submit-rejected /
 * submit-outcome-unknown，绝不会回退成 pending 或 cancelled。
 */
export type PublicStatus =
  | 'pending'
  | 'cancelled'
  | 'submit-outcome-unknown'
  | 'submitted'
  | 'submit-rejected';

export function publicStatusOf(rec: RecoveryRecord): PublicStatus {
  const outcome = rec.submission.outcome;
  if (outcome?.kind === 'accepted') return 'submitted';
  if (outcome?.kind === 'rejected') return 'submit-rejected';
  if (rec.submission.started) return 'submit-outcome-unknown';
  if (rec.phase === 'cancelled') return 'cancelled';
  return 'pending';
}

export interface JobContext {
  jobId: string;
  /** 仅在提交开始前有取消意义；提交开始后执行器忽略 abort，等待明确结果。 */
  signal: AbortSignal;
}

export interface JobDriver {
  prepare?(ctx: JobContext): Promise<void>;
  /** 必须以明确的 SubmissionOutcome 落定（resolve 或 reject 后由恢复流程查询）。 */
  submit(ctx: JobContext): Promise<SubmissionOutcome>;
  cleanup?(ctx: JobContext): Promise<void>;
  /** 恢复路径：当记录显示提交已开始但结果未知时，用 token 查询明确结果。 */
  resolveSubmission?(ctx: JobContext, token: string): Promise<SubmissionOutcome>;
}

export type Boundary = 'prepare' | 'submit' | 'cleanup';

export interface ExecutorOptions {
  /** 提交令牌，默认使用 jobId。 */
  token?: string;
  now?: () => number;
  /** 每次状态迁移后回调持久化快照（参数已是深拷贝，可直接 JSON 序列化）。 */
  onRecord?: (rec: RecoveryRecord) => void;
  /** 阶段边界钩子，便于在异步边界注入取消/故障（测试与观测用）。 */
  beforeBoundary?: (boundary: Boundary) => void | Promise<void>;
}

export type CancelResult =
  | 'cancelled' // 取消已生效（提交尚未开始）
  | 'deferred' // 提交已开始：取消被记录，但执行器继续等待明确结果
  | 'already-terminal';

class JobCancelledError extends Error {
  constructor() {
    super('job cancelled before submission started');
    this.name = 'JobCancelledError';
  }
}

function errMessage(err: unknown): string {
  return err instanceof Error ? `${err.name}: ${err.message}` : String(err);
}

function freshRecord(jobId: string, now: () => number): RecoveryRecord {
  return {
    version: 1,
    jobId,
    phase: 'idle',
    submission: { started: false, token: null, outcome: null },
    cleanup: { attempts: 0, lastError: null },
    cancelRequested: false,
    error: null,
    updatedAt: now(),
  };
}

export class StagedJobExecutor {
  private readonly record: RecoveryRecord;
  private readonly driver: JobDriver;
  private readonly opts: ExecutorOptions;
  private readonly now: () => number;
  private readonly token: string;
  private readonly controller = new AbortController();
  private readonly completion: Promise<RecoveryRecord>;

  private constructor(
    record: RecoveryRecord,
    driver: JobDriver,
    opts: ExecutorOptions,
    resume: boolean,
  ) {
    this.record = record;
    this.driver = driver;
    this.opts = opts;
    this.now = opts.now ?? (() => Date.now());
    this.token = opts.token ?? record.submission.token ?? record.jobId;
    // 推迟到微任务启动，保证 start()/resume() 先返回，
    // 调用方在边界钩子里能拿到执行器句柄（例如注入取消）。
    this.completion = Promise.resolve().then(() =>
      resume ? this.executeResume() : this.executeFresh(),
    );
  }

  /** 从头启动一次分阶段执行。 */
  static start(jobId: string, driver: JobDriver, opts: ExecutorOptions = {}): StagedJobExecutor {
    const now = opts.now ?? (() => Date.now());
    return new StagedJobExecutor(freshRecord(jobId, now), driver, opts, false);
  }

  /**
   * 从恢复记录继续执行。记录会先做一次 JSON 往返，
   * 以此证明恢复路径只依赖可序列化数据。
   */
  static resume(snapshot: RecoveryRecord, driver: JobDriver, opts: ExecutorOptions = {}): StagedJobExecutor {
    const record = JSON.parse(JSON.stringify(snapshot)) as RecoveryRecord;
    if (record.version !== 1) throw new Error(`unsupported record version: ${record.version}`);
    return new StagedJobExecutor(record, driver, opts, true);
  }

  /** 当前恢复记录的深拷贝快照（可 JSON 序列化）。 */
  snapshot(): RecoveryRecord {
    return JSON.parse(JSON.stringify(this.record)) as RecoveryRecord;
  }

  /** 当前对外状态。 */
  status(): PublicStatus {
    return publicStatusOf(this.record);
  }

  /**
   * 请求取消。
   * - 提交未开始：中止准备阶段，状态迁移到 cancelled。
   * - 提交已开始：仅记录 cancelRequested，返回 'deferred'，
   *   执行器继续等待提交的明确结果，随后仍会执行清理。
   */
  cancel(): CancelResult {
    const rec = this.record;
    if (rec.phase === 'done' || rec.phase === 'cancelled' || rec.phase === 'failed') {
      return 'already-terminal';
    }
    rec.cancelRequested = true;
    if (rec.submission.started) {
      this.persist();
      return 'deferred';
    }
    this.controller.abort();
    this.persist();
    return 'cancelled';
  }

  /** 整体落定（resolves with 最终恢复记录；失败也体现在记录中，不 rethrow）。 */
  done(): Promise<RecoveryRecord> {
    return this.completion.then(() => this.snapshot());
  }

  private ctx(): JobContext {
    return { jobId: this.record.jobId, signal: this.controller.signal };
  }

  private persist(): void {
    this.record.updatedAt = this.now();
    this.opts.onRecord?.(this.snapshot());
  }

  private setPhase(phase: Phase): void {
    this.record.phase = phase;
    this.persist();
  }

  private async boundary(boundary: Boundary): Promise<void> {
    await this.opts.beforeBoundary?.(boundary);
  }

  /** 提交开始前的取消检查点：仅在此处取消是安全的。 */
  private checkCancelledPreSubmit(): void {
    if (this.record.cancelRequested && !this.record.submission.started) {
      throw new JobCancelledError();
    }
  }

  private async executeFresh(): Promise<void> {
    const rec = this.record;
    try {
      await this.boundary('prepare');
      this.checkCancelledPreSubmit();
      this.setPhase('preparing');
      if (this.driver.prepare) await this.driver.prepare(this.ctx());

      await this.boundary('submit');
      this.checkCancelledPreSubmit();
      // 写前日志：先持久化"提交已开始 + 令牌"，再调用提交驱动。
      rec.submission.started = true;
      rec.submission.token = this.token;
      this.setPhase('submitting');
      // 提交一旦开始：忽略取消信号，必须等待明确结果。
      const outcome = await this.driver.submit(this.ctx());
      rec.submission.outcome = outcome;
      this.persist();

      await this.runCleanup();
      this.setPhase('done');
    } catch (err) {
      this.handleError(err);
    }
  }

  private async executeResume(): Promise<void> {
    const rec = this.record;
    try {
      if (!rec.submission.started) {
        // 提交从未开始：整体重跑是安全的（取消意图也会被重新检查）。
        await this.executeFresh();
        return;
      }
      if (rec.submission.outcome == null) {
        // 提交已开始但结果未知：必须查询明确结果，不得假设未执行。
        if (!this.driver.resolveSubmission) {
          throw new Error('driver.resolveSubmission is required to resume an in-flight submission');
        }
        const outcome = await this.driver.resolveSubmission(this.ctx(), rec.submission.token ?? this.token);
        rec.submission.outcome = outcome;
        this.persist();
      }
      if (rec.phase !== 'done') {
        await this.runCleanup();
        this.setPhase('done');
      }
    } catch (err) {
      this.handleError(err);
    }
  }

  /** 清理失败只记录错误，绝不清除提交状态。 */
  private async runCleanup(): Promise<void> {
    const rec = this.record;
    await this.boundary('cleanup');
    this.setPhase('cleaning');
    rec.cleanup.attempts += 1;
    try {
      if (this.driver.cleanup) await this.driver.cleanup(this.ctx());
      rec.cleanup.lastError = null;
    } catch (err) {
      rec.cleanup.lastError = errMessage(err);
    }
    this.persist();
  }

  private handleError(err: unknown): void {
    const rec = this.record;
    if (err instanceof JobCancelledError) {
      this.setPhase('cancelled');
      return;
    }
    if (rec.submission.started && rec.submission.outcome == null) {
      // 提交已开始但没有明确结果：保持 submitting 以便恢复查询，
      // 对外状态为 submit-outcome-unknown，绝不报告为未执行。
      rec.error = errMessage(err);
      this.setPhase('submitting');
      return;
    }
    if (rec.submission.outcome != null) {
      // 提交结果已明确：错误不得推翻提交状态。
      rec.error = errMessage(err);
      this.setPhase('done');
      return;
    }
    rec.error = errMessage(err);
    this.setPhase('failed');
  }
}
