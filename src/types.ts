/**
 * 轨迹差分工具 —— 类型定义
 *
 * 轨迹由「步骤事件」与「循环压缩段」组成；可观察动作由输入的
 * ObservableSpec 定义，其余步骤视为无副作用的内部步骤。
 */

/** 普通步骤事件 */
export interface StepEvent {
  kind: 'step';
  /** 动作名，例如 "http.request" / "cache.recompute" */
  action: string;
  /** 动作携带的值（可观察动作之间会比较该值） */
  value?: unknown;
  /** 附加信息（时间戳、线程等），不参与比较 */
  meta?: Record<string, unknown>;
}

/** 循环压缩段：body 重复 repeat 次，body 内可继续嵌套循环段 */
export interface LoopSegment {
  kind: 'loop';
  repeat: number;
  body: TraceEvent[];
}

export type TraceEvent = StepEvent | LoopSegment;

/**
 * 可观察动作定义（由输入给定）。
 * actions 中每项可以是精确动作名，或以 ".*" 结尾的前缀通配，
 * 例如 ["http.request", "db.*"]。
 */
export interface ObservableSpec {
  actions: string[];
}

/** 分歧类型 */
export type DivergenceKind =
  | 'value-mismatch'  // 同一可观察动作，值不同
  | 'missing-action'  // 一侧缺失某个可观察动作
  | 'out-of-order';   // 动作都存在但顺序不一致

/** 一个被提取出的可观察动作，附带其在原轨迹中的位置 */
export interface ObservableAction {
  action: string;
  value?: unknown;
  /** 在（惰性展开后的）轨迹中的步骤序号，从 0 开始 */
  stepIndex: number;
  /** 可观察动作序号，从 0 开始 */
  observableIndex: number;
  /** 人类可读的位置路径，例如 "trace[4].loop(第3轮)[1]" */
  path: string;
}

export interface Divergence {
  kind: DivergenceKind;
  /** 发生分歧的可观察动作序号 */
  observableIndex: number;
  /** A 侧当前可观察动作（若已耗尽则无） */
  a?: ObservableAction;
  /** B 侧当前可观察动作（若已耗尽则无） */
  b?: ObservableAction;
  /** 乱序判定时的前瞻命中信息 */
  lookahead?: {
    /** 在另一侧未来找到当前动作的偏移（以可观察动作计） */
    foundAtOffset: number;
    /** 前瞻是否因达到上限被截断 */
    truncated: boolean;
  };
  message: string;
}

/** 分歧点附近的内部（非可观察）步骤窗口 */
export interface InternalWindow {
  /** 分歧点之前最近的内部步骤（按时间升序） */
  before: StepEvent[];
  /** 分歧点之后紧随的内部步骤 */
  after: StepEvent[];
}

export interface DiffReport {
  equal: boolean;
  divergence?: Divergence;
  /** 分歧点相关的内部窗口；无分歧时为轨迹末尾窗口 */
  window: {
    a: InternalWindow;
    b: InternalWindow;
  };
  /** 各侧提取到的可观察动作总数（截至分歧点，含分歧动作） */
  observedCounts: { a: number; b: number };
}

export interface DiffOptions {
  /** 内部窗口大小（分歧点前后各保留多少内部步骤），默认 5 */
  windowSize?: number;
  /**
   * 乱序/缺失判定的前瞻上限（以可观察动作计），默认 256。
   * 前瞻是惰性求值的，不会展开整个循环压缩段。
   */
  lookaheadLimit?: number;
}
