import { Journal } from './journal.ts';
import { SystemClock, type Clock, type TimerHandle } from './clock.ts';
import type {
  JournalEvent,
  SagaDefinition,
  StepContext,
  StepSnapshot,
  StepState,
  StoredJournalEvent,
  TxSnapshot,
  TxStatus,
} from './types.ts';

interface TxRecord {
  txId: string;
  sagaName: string;
  status: TxStatus;
  currentStepIndex: number;
  deadline?: number;
  data: Record<string, unknown>;
  steps: StepSnapshot[];
}

/**
 * 长事务补偿（Saga）引擎。
 *
 * 状态机：每步 PENDING -> RUNNING -> (COMPLETED | WAITING -> COMPLETED | FAILED | TIMED_OUT)
 * 事务级：RUNNING -> WAITING_EXTERNAL -> ... -> COMPLETED
 *                \-> (失败/超时) -> COMPENSATING -> COMPENSATED（补偿失败则 FAILED）
 *
 * 持久化：所有迁移先写追加式日志再推进，重启后 recover() 重放日志并续跑：
 *  - RUNNING 中崩溃：以同一幂等键重放动作（不重）
 *  - WAITING 中崩溃：按原截止时间重挂超时定时器（不漏）
 *  - COMPENSATING 中崩溃：从未完成的补偿步骤继续
 */
export class SagaEngine {
  private readonly journal: Journal;
  private readonly clock: Clock;
  private readonly sagas = new Map<string, SagaDefinition>();
  private readonly txs = new Map<string, TxRecord>();
  private readonly timers = new Map<string, TimerHandle>();
  private pendingOps = 0;
  private idleResolvers: Array<() => void> = [];

  constructor(options: { journalPath: string; clock?: Clock }) {
    this.journal = new Journal(options.journalPath);
    this.clock = options.clock ?? new SystemClock();
  }

  registerSaga(saga: SagaDefinition): void {
    this.sagas.set(saga.name, saga);
  }

  /** 注册完 Saga 后调用：重放日志，恢复所有未完成事务 */
  recover(): void {
    for (const event of this.journal.all()) {
      this.apply(event);
    }
    for (const record of this.txs.values()) {
      this.resume(record);
    }
  }

  startTransaction(sagaName: string, txId: string, data: Record<string, unknown> = {}): void {
    const saga = this.sagas.get(sagaName);
    if (!saga) throw new Error(`unknown saga: ${sagaName}`);
    if (this.txs.has(txId)) throw new Error(`duplicate txId: ${txId}`);
    const record: TxRecord = {
      txId,
      sagaName,
      status: 'RUNNING',
      currentStepIndex: 0,
      data,
      steps: saga.steps.map((s) => ({ name: s.name, state: 'PENDING' as StepState })),
    };
    this.txs.set(txId, record);
    this.journal.append({
      type: 'TX_STARTED',
      txId,
      saga: sagaName,
      data,
      steps: saga.steps.map((s) => s.name),
    });
    void this.track(this.beginStep(record, 0));
  }

  /** 外部系统回调：步骤完成（如银行付款成功通知）。迟到/重复回调返回 false 并被忽略 */
  resolveExternal(txId: string, stepName: string, output?: unknown): boolean {
    const found = this.expectWaiting(txId, stepName);
    if (!found) return false;
    const [record, index] = found;
    this.completeStep(record, index, output);
    void this.track(this.advance(record, index + 1));
    return true;
  }

  /** 外部系统回调：步骤被拒绝（如银行拒付），触发反向补偿 */
  rejectExternal(txId: string, stepName: string, error: string): boolean {
    const found = this.expectWaiting(txId, stepName);
    if (!found) return false;
    const [record, index] = found;
    this.clearTimer(txId);
    record.steps[index].state = 'FAILED';
    this.journal.append({ type: 'STEP_FAILED', txId, index, step: stepName, error });
    void this.track(this.compensate(record, index - 1, `step "${stepName}" rejected: ${error}`));
    return true;
  }

  getTransaction(txId: string): TxSnapshot | undefined {
    const record = this.txs.get(txId);
    return record ? structuredClone(record) : undefined;
  }

  /** 全链路可复核审计轨迹 */
  getAuditTrail(txId: string): StoredJournalEvent[] {
    return this.journal.forTx(txId);
  }

  /** 等待当前所有在飞操作沉降（测试与演示用） */
  whenIdle(): Promise<void> {
    if (this.pendingOps === 0) return Promise.resolve();
    return new Promise((resolve) => this.idleResolvers.push(resolve));
  }

  /** 清理定时器（模拟进程退出/断电） */
  shutdown(): void {
    for (const handle of this.timers.values()) {
      this.clock.clearTimeout(handle);
    }
    this.timers.clear();
  }

  // ---- 内部实现 ----

  private expectWaiting(txId: string, stepName: string): [TxRecord, number] | undefined {
    const record = this.txs.get(txId);
    if (!record || record.status !== 'WAITING_EXTERNAL') return undefined;
    const index = record.currentStepIndex;
    const step = record.steps[index];
    if (step.name !== stepName || step.state !== 'WAITING') return undefined;
    return [record, index];
  }

  private makeContext(record: TxRecord, index: number): StepContext {
    const stepDef = this.sagas.get(record.sagaName)!.steps[index];
    return {
      txId: record.txId,
      stepName: stepDef.name,
      idempotencyKey: `${record.txId}:${stepDef.name}`,
      data: record.data,
    };
  }

  private track(p: Promise<void>): Promise<void> {
    this.pendingOps++;
    const done = () => {
      this.pendingOps--;
      if (this.pendingOps === 0) {
        const resolvers = this.idleResolvers.splice(0);
        for (const resolve of resolvers) resolve();
      }
    };
    p.then(done, done);
    return p;
  }

  private armTimer(txId: string, index: number, deadline: number): void {
    this.clearTimer(txId);
    const delay = Math.max(0, deadline - this.clock.now());
    const handle = this.clock.setTimeout(() => {
      void this.track(this.onTimeout(txId, index, deadline));
    }, delay);
    this.timers.set(txId, handle);
  }

  private clearTimer(txId: string): void {
    const handle = this.timers.get(txId);
    if (handle) {
      this.clock.clearTimeout(handle);
      this.timers.delete(txId);
    }
  }

  private async beginStep(record: TxRecord, index: number): Promise<void> {
    const saga = this.sagas.get(record.sagaName)!;
    const stepDef = saga.steps[index];
    record.status = 'RUNNING';
    record.currentStepIndex = index;
    record.steps[index].state = 'RUNNING';
    record.deadline = this.clock.now() + stepDef.timeoutMs;
    this.journal.append({
      type: 'STEP_STARTED',
      txId: record.txId,
      index,
      step: stepDef.name,
      deadline: record.deadline,
    });
    this.armTimer(record.txId, index, record.deadline);
    await this.executeAction(record, index);
  }

  private async executeAction(record: TxRecord, index: number): Promise<void> {
    const saga = this.sagas.get(record.sagaName)!;
    const stepDef = saga.steps[index];
    let result;
    try {
      result = await stepDef.action(this.makeContext(record, index));
    } catch (err) {
      if (record.steps[index].state !== 'RUNNING') return; // 动作在飞时已超时
      this.clearTimer(record.txId);
      record.steps[index].state = 'FAILED';
      const message = err instanceof Error ? err.message : String(err);
      this.journal.append({ type: 'STEP_FAILED', txId: record.txId, index, step: stepDef.name, error: message });
      await this.compensate(record, index - 1, `step "${stepDef.name}" failed: ${message}`);
      return;
    }
    if (record.steps[index].state !== 'RUNNING') return; // 动作在飞时已超时
    if (result.type === 'pending') {
      record.status = 'WAITING_EXTERNAL';
      record.steps[index].state = 'WAITING';
      this.journal.append({ type: 'STEP_WAITING', txId: record.txId, index, step: stepDef.name });
      return;
    }
    this.completeStep(record, index, result.output);
    await this.advance(record, index + 1);
  }

  private completeStep(record: TxRecord, index: number, output: unknown): void {
    this.clearTimer(record.txId);
    record.steps[index].state = 'COMPLETED';
    record.steps[index].output = output;
    this.journal.append({
      type: 'STEP_COMPLETED',
      txId: record.txId,
      index,
      step: record.steps[index].name,
      output,
    });
  }

  private async advance(record: TxRecord, nextIndex: number): Promise<void> {
    const saga = this.sagas.get(record.sagaName)!;
    if (nextIndex >= saga.steps.length) {
      record.status = 'COMPLETED';
      this.journal.append({ type: 'TX_COMPLETED', txId: record.txId });
      return;
    }
    await this.beginStep(record, nextIndex);
  }

  private async onTimeout(txId: string, index: number, deadline: number): Promise<void> {
    const record = this.txs.get(txId);
    if (!record) return;
    if (record.currentStepIndex !== index || record.deadline !== deadline) return; // 过期定时器
    const step = record.steps[index];
    if (step.state !== 'RUNNING' && step.state !== 'WAITING') return;
    this.timers.delete(txId);
    step.state = 'TIMED_OUT';
    this.journal.append({ type: 'STEP_TIMED_OUT', txId, index, step: step.name, deadline });
    await this.compensate(record, index - 1, `step "${step.name}" timed out`);
  }

  private async compensate(record: TxRecord, fromIndex: number, reason: string): Promise<void> {
    if (record.status !== 'COMPENSATING') {
      record.status = 'COMPENSATING';
      this.journal.append({ type: 'COMPENSATION_STARTED', txId: record.txId, reason });
    }
    const saga = this.sagas.get(record.sagaName)!;
    for (let i = fromIndex; i >= 0; i--) {
      const stepRec = record.steps[i];
      // COMPENSATING 状态 = 崩溃在补偿动作执行中，重启后幂等重放
      if (stepRec.state !== 'COMPLETED' && stepRec.state !== 'COMPENSATING') continue;
      const stepDef = saga.steps[i];
      stepRec.state = 'COMPENSATING';
      this.journal.append({ type: 'COMPENSATION_STEP_STARTED', txId: record.txId, index: i, step: stepDef.name });
      try {
        await stepDef.compensate?.(this.makeContext(record, i));
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        record.status = 'FAILED';
        this.journal.append({
          type: 'TX_FAILED',
          txId: record.txId,
          error: `compensation for "${stepDef.name}" failed: ${message}`,
        });
        return;
      }
      stepRec.state = 'COMPENSATED';
      this.journal.append({ type: 'COMPENSATION_STEP_DONE', txId: record.txId, index: i, step: stepDef.name });
    }
    record.status = 'COMPENSATED';
    this.journal.append({ type: 'TX_COMPENSATED', txId: record.txId });
  }

  private resume(record: TxRecord): void {
    switch (record.status) {
      case 'COMPLETED':
      case 'COMPENSATED':
      case 'FAILED':
        return;
      case 'RUNNING': {
        // 崩溃在动作执行中：截止已过则直接超时，否则重挂定时器并幂等重放动作
        const index = record.currentStepIndex;
        const deadline = record.deadline!;
        if (deadline - this.clock.now() <= 0) {
          void this.track(this.onTimeout(record.txId, index, deadline));
        } else {
          this.armTimer(record.txId, index, deadline);
          void this.track(this.executeAction(record, index));
        }
        return;
      }
      case 'WAITING_EXTERNAL': {
        // 崩溃在等待外部回调：外部请求已发出，只重挂超时定时器，不重复调用
        const index = record.currentStepIndex;
        const deadline = record.deadline!;
        if (deadline - this.clock.now() <= 0) {
          void this.track(this.onTimeout(record.txId, index, deadline));
        } else {
          this.armTimer(record.txId, index, deadline);
        }
        return;
      }
      case 'COMPENSATING': {
        void this.track(this.compensate(record, record.steps.length - 1, 'resume compensation after restart'));
        return;
      }
    }
  }

  private apply(event: JournalEvent): void {
    switch (event.type) {
      case 'TX_STARTED': {
        this.txs.set(event.txId, {
          txId: event.txId,
          sagaName: event.saga,
          status: 'RUNNING',
          currentStepIndex: 0,
          data: event.data,
          steps: event.steps.map((name) => ({ name, state: 'PENDING' as StepState })),
        });
        return;
      }
    }
    const record = this.txs.get(event.txId);
    if (!record) return;
    switch (event.type) {
      case 'STEP_STARTED':
        record.status = 'RUNNING';
        record.currentStepIndex = event.index;
        record.deadline = event.deadline;
        record.steps[event.index].state = 'RUNNING';
        return;
      case 'STEP_WAITING':
        record.status = 'WAITING_EXTERNAL';
        record.steps[event.index].state = 'WAITING';
        return;
      case 'STEP_COMPLETED':
        record.steps[event.index].state = 'COMPLETED';
        record.steps[event.index].output = event.output;
        return;
      case 'STEP_FAILED':
        record.steps[event.index].state = 'FAILED';
        return;
      case 'STEP_TIMED_OUT':
        record.steps[event.index].state = 'TIMED_OUT';
        return;
      case 'COMPENSATION_STARTED':
        record.status = 'COMPENSATING';
        return;
      case 'COMPENSATION_STEP_STARTED':
        record.steps[event.index].state = 'COMPENSATING';
        return;
      case 'COMPENSATION_STEP_DONE':
        record.steps[event.index].state = 'COMPENSATED';
        return;
      case 'TX_COMPLETED':
        record.status = 'COMPLETED';
        return;
      case 'TX_COMPENSATED':
        record.status = 'COMPENSATED';
        return;
      case 'TX_FAILED':
        record.status = 'FAILED';
        return;
    }
  }
}
