/**
 * 场景输入模型：任务 = 基础优先级 + 离散执行片段序列。
 * 优先级数值越大越高。时间以离散的 tick（时间片）为单位。
 */

export type Segment =
  | { type: "compute"; duration: number }
  | { type: "acquire"; lock: string; timeout?: number; onTimeout?: "abort" | "continue" }
  | { type: "release"; lock: string };

export interface TaskSpec {
  id: string;
  /** 基础优先级，数值越大优先级越高 */
  priority: number;
  /** 释放（到达）时间，默认 0 */
  releaseTime?: number;
  segments: Segment[];
}

export interface Scenario {
  name?: string;
  /** 仿真 tick 上限，防止意外死循环，默认 10000 */
  maxTicks?: number;
  tasks: TaskSpec[];
}

export type TaskState = "pending" | "ready" | "blocked" | "done";
