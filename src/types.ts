/** 步骤执行结果：done = 同步完成；pending = 已发起外部调用，等待异步回调 */
export type StepActionResult =
  | { type: 'done'; output?: unknown }
  | { type: 'pending' };

export interface StepContext {
  txId: string;
  stepName: string;
  /** 幂等键：同一事务同一步骤恒定，外部系统据此去重，保证断电重放"不重" */
  idempotencyKey: string;
  data: Record<string, unknown>;
}

export interface StepDefinition {
  name: string;
  /** 步骤超时（毫秒）。超时未完成的步骤触发反向补偿 */
  timeoutMs: number;
  /** 正向动作：状态迁移 + 外部调用 */
  action: (ctx: StepContext) => Promise<StepActionResult>;
  /** 反向补偿动作（可选，但参与 Saga 的步骤通常都应提供） */
  compensate?: (ctx: StepContext) => Promise<void>;
}

export interface SagaDefinition {
  name: string;
  steps: StepDefinition[];
}

export type StepState =
  | 'PENDING'
  | 'RUNNING'
  | 'WAITING'
  | 'COMPLETED'
  | 'FAILED'
  | 'TIMED_OUT'
  | 'COMPENSATING'
  | 'COMPENSATED';

export type TxStatus =
  | 'RUNNING'
  | 'WAITING_EXTERNAL'
  | 'COMPENSATING'
  | 'COMPLETED'
  | 'COMPENSATED'
  | 'FAILED';

export interface StepSnapshot {
  name: string;
  state: StepState;
  output?: unknown;
}

export interface TxSnapshot {
  txId: string;
  sagaName: string;
  status: TxStatus;
  currentStepIndex: number;
  deadline?: number;
  data: Record<string, unknown>;
  steps: StepSnapshot[];
}

/** 审计日志事件（追加式写入 JSONL 日志文件，即状态机的持久化形式） */
export type JournalEvent =
  | { type: 'TX_STARTED'; txId: string; saga: string; data: Record<string, unknown>; steps: string[] }
  | { type: 'STEP_STARTED'; txId: string; index: number; step: string; deadline: number }
  | { type: 'STEP_WAITING'; txId: string; index: number; step: string }
  | { type: 'STEP_COMPLETED'; txId: string; index: number; step: string; output?: unknown }
  | { type: 'STEP_FAILED'; txId: string; index: number; step: string; error: string }
  | { type: 'STEP_TIMED_OUT'; txId: string; index: number; step: string; deadline: number }
  | { type: 'COMPENSATION_STARTED'; txId: string; reason: string }
  | { type: 'COMPENSATION_STEP_STARTED'; txId: string; index: number; step: string }
  | { type: 'COMPENSATION_STEP_DONE'; txId: string; index: number; step: string }
  | { type: 'TX_COMPLETED'; txId: string }
  | { type: 'TX_COMPENSATED'; txId: string }
  | { type: 'TX_FAILED'; txId: string; error: string };

export type StoredJournalEvent = JournalEvent & { seq: number; ts: number };
