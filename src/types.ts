/** 步骤执行结果：done 表示同步完成；wait 表示挂起等待外部回调（如银行回执）。 */
export type StepResult =
  | { kind: 'done'; output?: unknown }
  | { kind: 'wait'; token?: unknown };

/** 外部回调结果：completed 正常完成；rejected 外部拒绝，触发补偿。 */
export type CallbackOutcome =
  | { status: 'completed'; output?: unknown }
  | { status: 'rejected'; reason: string };

export interface StepContext {
  instanceId: string;
  stepName: string;
  input: unknown;
  /** 跨步骤传递的业务数据（如前序步骤产出的订单号）。 */
  data: Record<string, unknown>;
}

export interface StepDefinition {
  name: string;
  /** 正向动作：状态迁移 + 外部调用。 */
  action: (ctx: StepContext) => Promise<StepResult> | StepResult;
  /** 逆向补偿动作（可选）。 */
  compensate?: (ctx: StepContext) => Promise<void> | void;
  /** wait 状态下等待外部回调的超时时间（毫秒）。超时触发补偿。 */
  timeoutMs?: number;
}

export interface SagaDefinition {
  name: string;
  steps: StepDefinition[];
}

export type InstanceStatus =
  | 'running'
  | 'waiting'
  | 'compensating'
  | 'completed'
  | 'compensated'
  | 'failed';

export interface WaitingInfo {
  stepIndex: number;
  deadline: number;
}

/** 状态机实例（由审计事件流折叠重建，可持久化、可恢复）。 */
export interface InstanceState {
  id: string;
  saga: string;
  status: InstanceStatus;
  input: unknown;
  data: Record<string, unknown>;
  stepIndex: number;
  completedSteps: number[];
  compensatedSteps: number[];
  waiting: WaitingInfo | null;
  error: string | null;
}

/** 审计事件：全链路可复核轨迹，append-only 持久化。 */
export type AuditEvent =
  | { type: 'instance_started'; at: number; instanceId: string; saga: string; input: unknown }
  | { type: 'step_started'; at: number; instanceId: string; step: string; stepIndex: number }
  | { type: 'step_completed'; at: number; instanceId: string; step: string; stepIndex: number; output: unknown; data?: Record<string, unknown> }
  | { type: 'step_waiting'; at: number; instanceId: string; step: string; stepIndex: number; deadline: number; data?: Record<string, unknown> }
  | { type: 'callback_received'; at: number; instanceId: string; step: string; stepIndex: number; outcome: CallbackOutcome }
  | { type: 'step_failed'; at: number; instanceId: string; step: string; stepIndex: number; error: string }
  | { type: 'timeout_fired'; at: number; instanceId: string; step: string; stepIndex: number }
  | { type: 'compensation_started'; at: number; instanceId: string; reason: string }
  | { type: 'compensation_step_started'; at: number; instanceId: string; step: string; stepIndex: number }
  | { type: 'compensation_step_completed'; at: number; instanceId: string; step: string; stepIndex: number }
  | { type: 'instance_completed'; at: number; instanceId: string }
  | { type: 'instance_compensated'; at: number; instanceId: string }
  | { type: 'instance_failed'; at: number; instanceId: string; error: string };
