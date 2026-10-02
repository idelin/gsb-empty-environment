/**
 * 任务排期场景:5 个任务分配到 3 个时段。
 *
 * 硬约束(3 条,绝对成立):
 *   H1 设备A冲突:  T1(需求评审) 与 T2(原型开发) 共用设备A,不能同时段
 *   H2 设备B冲突:  T3(接口联调) 与 T4(压力测试) 共用设备B,不能同时段
 *   H3 人员冲突:   T5(发布部署) 与 T2(原型开发) 由同一名工程师负责,不能同时段
 *
 * 软约束(5 条,违反付出权重惩罚,均为"尽量安排在周一上午"):
 *   S1 w=10  T1 尽量周一上午(客户到场,最重要)
 *   S2 w=8   T3 尽量周一上午(外部团队仅此时在线)
 *   S3 w=3   T5 尽量周一上午
 *   S4 w=2   T2 尽量周一上午
 *   S5 w=1   T4 尽量周一上午
 *
 * 冲突结构:H1 使 S1 与 S4 至多满足其一;H2 使 S2 与 S5 至多满足其一。
 * 因此任何排期至少违反 {S1,S4} 之一(代价 >= 2)且至少违反 {S2,S5} 之一
 * (代价 >= 1),总惩罚下界为 3。全局最优:违反两条最次要的软约束
 * S4(w=2) 与 S5(w=1),总惩罚 = 3。
 */

import type { Assignment, MaxCSPProblem } from "./solver.ts";

export const SLOT_NAMES: Record<number, string> = {
  1: "周一上午",
  2: "周一下午",
  3: "周二上午",
};

export const TASK_NAMES: Record<string, string> = {
  T1: "需求评审",
  T2: "原型开发",
  T3: "接口联调",
  T4: "压力测试",
  T5: "发布部署",
};

export function buildSchedulingProblem(): MaxCSPProblem {
  const tasks = ["T1", "T2", "T3", "T4", "T5"];
  const slots = [1, 2, 3];

  const neq =
    (a: string, b: string) =>
    (asn: Assignment): boolean =>
      asn[a] === undefined || asn[b] === undefined || asn[a] !== asn[b];

  const preferSlot = (task: string, slot: number) => (asn: Assignment): boolean =>
    asn[task] !== undefined && asn[task] !== slot;

  return {
    variables: tasks.map((name) => ({ name, domain: slots })),
    hard: [
      { name: "H1", scope: ["T1", "T2"], consistent: neq("T1", "T2") },
      { name: "H2", scope: ["T3", "T4"], consistent: neq("T3", "T4") },
      { name: "H3", scope: ["T5", "T2"], consistent: neq("T5", "T2") },
    ],
    soft: [
      { name: "S1", weight: 10, scope: ["T1"], violated: preferSlot("T1", 1) },
      { name: "S2", weight: 8, scope: ["T3"], violated: preferSlot("T3", 1) },
      { name: "S3", weight: 3, scope: ["T5"], violated: preferSlot("T5", 1) },
      { name: "S4", weight: 2, scope: ["T2"], violated: preferSlot("T2", 1) },
      { name: "S5", weight: 1, scope: ["T4"], violated: preferSlot("T4", 1) },
    ],
  };
}
