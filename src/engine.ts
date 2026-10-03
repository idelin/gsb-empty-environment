import { randomUUID } from 'node:crypto';
import { EventStore } from './store.ts';
import type {
  AuditEvent,
  CallbackOutcome,
  InstanceState,
  SagaDefinition,
  StepContext,
} from './types.ts';

const TERMINAL = new Set(['completed', 'compensated', 'failed']);

/** 由事件流折叠出实例状态（恢复与运行时共用同一套迁移逻辑）。 */
function applyEvent(state: InstanceState, event: AuditEvent): void {
  switch (event.type) {
    case 'step_started':
      state.stepIndex = event.stepIndex;
      state.status = 'running';
      break;
    case 'step_completed':
      if (!state.completedSteps.includes(event.stepIndex)) {
        state.completedSteps.push(event.stepIndex);
      }
      state.waiting = null;
      if (event.data) state.data = { ...event.data };
      break;
    case 'step_waiting':
      state.status = 'waiting';
      state.waiting = { stepIndex: event.stepIndex, deadline: event.deadline };
      if (event.data) state.data = { ...event.data };
      break;
    case 'callback_received':
      state.waiting = null;
      break;
    case 'step_failed':
      state.error = event.error;
      break;
    case 'timeout_fired':
      state.waiting = null;
      break;
    case 'compensation_started':
      state.status = 'compensating';
      break;
    case 'compensation_step_completed':
      if (!state.compensatedSteps.includes(event.stepIndex)) {
        state.compensatedSteps.push(event.stepIndex);
      }
      break;
    case 'instance_completed':
      state.status = 'completed';
      break;
    case 'instance_compensated':
      state.status = 'compensated';
      break;
    case 'instance_failed':
      state.status = 'failed';
      state.error = event.error;
      break;
  }
}

/**
 * 长事务补偿与状态机持久化引擎。
 *
 * - 每个 Saga 实例是一条状态机：running → waiting → (completed | compensating → compensated | failed)。
 * - 所有状态迁移先写审计日志（同步落盘）再生效，断电后 recover() 重放日志精确续跑：
 *   已完成的步骤绝不重跑，等待中的超时定时器按原截止时间恢复，补偿从断点继续。
 */
export class SagaEngine {
  private readonly store: EventStore;
  private readonly sagas = new Map<string, SagaDefinition>();
  private readonly instances = new Map<string, InstanceState>();
  private readonly timers = new Map<string, NodeJS.Timeout>();
  private readonly chains = new Map<string, Promise<void>>();
  private readonly waiters = new Map<string, Array<() => void>>();

  constructor(dir: string) {
    this.store = new EventStore(dir);
  }

  registerSaga(def: SagaDefinition): void {
    this.sagas.set(def.name, def);
  }

  /** 重放审计日志，恢复所有未终结实例并继续推进。 */
  async recover(): Promise<void> {
    for (const event of this.store.all()) {
      if (event.type === 'instance_started') {
        this.instances.set(event.instanceId, {
          id: event.instanceId,
          saga: event.saga,
          status: 'running',
          input: event.input,
          data: {},
          stepIndex: 0,
          completedSteps: [],
          compensatedSteps: [],
          waiting: null,
          error: null,
        });
      } else {
        const state = this.instances.get(event.instanceId);
        if (state) applyEvent(state, event);
      }
    }
    for (const state of this.instances.values()) {
      if (TERMINAL.has(state.status)) continue;
      if (state.status === 'waiting' && state.waiting) {
        this.armTimer(state.id, state.waiting.stepIndex, state.waiting.deadline);
      } else if (state.status === 'running') {
        this.enqueue(state.id, () => this.runForward(state.id));
      } else if (state.status === 'compensating') {
        this.enqueue(state.id, () => this.runCompensation(state.id));
      }
    }
    await this.settled();
  }

  /** 发起长事务，返回实例 ID；引擎异步推进到下一个挂起点或终态。 */
  async start(sagaName: string, input?: unknown): Promise<string> {
    const def = this.sagas.get(sagaName);
    if (!def) throw new Error(`未注册的 Saga: ${sagaName}`);
    const id = randomUUID();
    this.emit({
      type: 'instance_started',
      at: Date.now(),
      instanceId: id,
      saga: sagaName,
      input: input ?? null,
    });
    this.instances.set(id, {
      id,
      saga: sagaName,
      status: 'running',
      input: input ?? null,
      data: {},
      stepIndex: 0,
      completedSteps: [],
      compensatedSteps: [],
      waiting: null,
      error: null,
    });
    this.enqueue(id, () => this.runForward(id));
    return id;
  }

  /** 外部系统回调（如银行回执）：completed 继续正向流程，rejected 触发逆向补偿。 */
  notify(instanceId: string, outcome: CallbackOutcome): void {
    this.enqueue(instanceId, async () => {
      const state = this.mustGet(instanceId);
      if (state.status !== 'waiting' || !state.waiting) return;
      const waitIndex = state.waiting.stepIndex;
      const def = this.sagas.get(state.saga)!;
      const step = def.steps[waitIndex];
      this.clearTimer(instanceId);
      this.emit({
        type: 'callback_received',
        at: Date.now(),
        instanceId,
        step: step.name,
        stepIndex: waitIndex,
        outcome,
      });
      if (outcome.status === 'rejected') {
        this.emit({
          type: 'step_failed',
          at: Date.now(),
          instanceId,
          step: step.name,
          stepIndex: waitIndex,
          error: `外部拒绝: ${outcome.reason}`,
        });
        await this.beginCompensation(instanceId, `步骤 ${step.name} 被外部拒绝: ${outcome.reason}`);
        return;
      }
      this.emit({
        type: 'step_completed',
        at: Date.now(),
        instanceId,
        step: step.name,
        stepIndex: waitIndex,
        output: outcome.output ?? null,
        data: { ...state.data },
      });
      state.stepIndex = waitIndex + 1;
      state.status = 'running';
      await this.runForward(instanceId);
    });
  }

  getState(instanceId: string): InstanceState | undefined {
    const s = this.instances.get(instanceId);
    return s ? { ...s, data: { ...s.data } } : undefined;
  }

  /** 全链路审计轨迹（可指定实例过滤）。 */
  auditTrail(instanceId?: string): AuditEvent[] {
    const all = this.store.all();
    return instanceId ? all.filter((e) => e.instanceId === instanceId) : all;
  }

  /** 等待实例到达终态（completed / compensated / failed）。 */
  waitFor(instanceId: string): Promise<InstanceState> {
    const state = this.mustGet(instanceId);
    if (TERMINAL.has(state.status)) return Promise.resolve(this.getState(instanceId)!);
    return new Promise((resolve) => {
      this.waiters.set(instanceId, [
        ...(this.waiters.get(instanceId) ?? []),
        () => resolve(this.getState(instanceId)!),
      ]);
    });
  }

  /** 等待所有在途实例推进到挂起点或终态（测试与优雅停机用）。 */
  async settled(): Promise<void> {
    await Promise.all([...this.chains.values()]);
  }

  /** 模拟断电/优雅停机：清除定时器并等待在途任务落盘。 */
  async shutdown(): Promise<void> {
    for (const timer of this.timers.values()) clearTimeout(timer);
    this.timers.clear();
    await this.settled();
  }

  private mustGet(id: string): InstanceState {
    const state = this.instances.get(id);
    if (!state) throw new Error(`未知实例: ${id}`);
    return state;
  }

  private emit(event: AuditEvent): void {
    this.store.append(event);
    const state = this.instances.get(event.instanceId);
    if (state && event.type !== 'instance_started') applyEvent(state, event);
    if (TERMINAL.has(this.instances.get(event.instanceId)?.status ?? '')) {
      for (const wake of this.waiters.get(event.instanceId) ?? []) wake();
      this.waiters.delete(event.instanceId);
    }
  }

  private enqueue(id: string, fn: () => Promise<void>): void {
    const prev = this.chains.get(id) ?? Promise.resolve();
    const next = prev.then(fn).catch((err) => {
      console.error(`实例 ${id} 推进失败:`, err);
    });
    this.chains.set(id, next);
  }

  private armTimer(id: string, stepIndex: number, deadline: number): void {
    this.clearTimer(id);
    const delay = Math.max(0, deadline - Date.now());
    this.timers.set(
      id,
      setTimeout(() => {
        this.enqueue(id, async () => {
          const state = this.mustGet(id);
          if (state.status !== 'waiting' || state.waiting?.stepIndex !== stepIndex) return;
          const def = this.sagas.get(state.saga)!;
          const step = def.steps[stepIndex];
          this.emit({ type: 'timeout_fired', at: Date.now(), instanceId: id, step: step.name, stepIndex });
          await this.beginCompensation(id, `步骤 ${step.name} 等待外部回调超时`);
        });
      }, delay),
    );
  }

  private clearTimer(id: string): void {
    const timer = this.timers.get(id);
    if (timer) clearTimeout(timer);
    this.timers.delete(id);
  }

  private ctx(state: InstanceState, stepName: string): StepContext {
    return { instanceId: state.id, stepName, input: state.input, data: state.data };
  }

  private async runForward(id: string): Promise<void> {
    const state = this.mustGet(id);
    const def = this.sagas.get(state.saga)!;
    while (state.stepIndex < def.steps.length) {
      const step = def.steps[state.stepIndex];
      const index = state.stepIndex;
      this.emit({ type: 'step_started', at: Date.now(), instanceId: id, step: step.name, stepIndex: index });
      let result;
      try {
        result = await step.action(this.ctx(state, step.name));
      } catch (err) {
        this.emit({
          type: 'step_failed',
          at: Date.now(),
          instanceId: id,
          step: step.name,
          stepIndex: index,
          error: err instanceof Error ? err.message : String(err),
        });
        await this.beginCompensation(id, `步骤 ${step.name} 执行异常`);
        return;
      }
      if (result.kind === 'wait') {
        const deadline = Date.now() + (step.timeoutMs ?? 60_000);
        this.emit({
          type: 'step_waiting',
          at: Date.now(),
          instanceId: id,
          step: step.name,
          stepIndex: index,
          deadline,
          data: { ...state.data },
        });
        this.armTimer(id, index, deadline);
        return;
      }
      this.emit({
        type: 'step_completed',
        at: Date.now(),
        instanceId: id,
        step: step.name,
        stepIndex: index,
        output: result.output ?? null,
        data: { ...state.data },
      });
      state.stepIndex = index + 1;
    }
    this.emit({ type: 'instance_completed', at: Date.now(), instanceId: id });
  }

  private async beginCompensation(id: string, reason: string): Promise<void> {
    this.emit({ type: 'compensation_started', at: Date.now(), instanceId: id, reason });
    await this.runCompensation(id);
  }

  private async runCompensation(id: string): Promise<void> {
    const state = this.mustGet(id);
    const def = this.sagas.get(state.saga)!;
    const pending = [...state.completedSteps]
      .filter((i) => !state.compensatedSteps.includes(i))
      .sort((a, b) => b - a);
    for (const index of pending) {
      const step = def.steps[index];
      if (!step.compensate) continue;
      this.emit({
        type: 'compensation_step_started',
        at: Date.now(),
        instanceId: id,
        step: step.name,
        stepIndex: index,
      });
      try {
        await step.compensate(this.ctx(state, step.name));
      } catch (err) {
        this.emit({
          type: 'instance_failed',
          at: Date.now(),
          instanceId: id,
          error: `补偿步骤 ${step.name} 失败: ${err instanceof Error ? err.message : String(err)}`,
        });
        return;
      }
      this.emit({
        type: 'compensation_step_completed',
        at: Date.now(),
        instanceId: id,
        step: step.name,
        stepIndex: index,
      });
    }
    this.emit({ type: 'instance_compensated', at: Date.now(), instanceId: id });
  }
}
