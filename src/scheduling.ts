/**
 * 任务排期场景：5 个任务排进 5 个工作日时段（周一至周五），每个任务占 1 个时段。
 *
 * 硬约束（必须绝对成立，共 3 条）：
 *   H1 资源独占 —— 同一时段最多安排一个任务；
 *   H2 硬性截止 —— 「需求评审」必须安排在周三（含）之前；
 *   H3 合规前置 —— 「宣传物料」必须早于「版本上线」（法务要求物料先审后发）。
 *
 * 软约束（允许违反，违反计入惩罚权重，共 5 条）：
 *   S1 权重 10 —— 需求评审排在周一（关键干系人仅周一有空）；
 *   S2 权重 8  —— 原型设计排在周二（设计团队冲刺节奏）；
 *   S3 权重 2  —— 版本上线排在周三（次要：避开周五发布的惯例偏好）；
 *   S4 权重 4  —— 宣传物料排在周四（市场活动节点）；
 *   S5 权重 1  —— 备份演练排在版本上线后第 2 天（次要：运维偏好）。
 *
 * 该场景下 5 条软约束无法同时满足（S1+S2+S3+S4 会与 H3 冲突），
 * 全局最优解恰好违反权重最低的两条次要软约束 S3、S5，总惩罚 3。
 */

import { MaxCSPSolver, type Assignment, type Solution } from "./maxcsp.ts";

export const SLOT_NAMES = ["周一", "周二", "周三", "周四", "周五"] as const;

export interface TaskDef {
  id: string;
  label: string;
}

export const TASKS: readonly TaskDef[] = [
  { id: "T1", label: "需求评审" },
  { id: "T2", label: "原型设计" },
  { id: "T3", label: "版本上线" },
  { id: "T4", label: "宣传物料" },
  { id: "T5", label: "备份演练" },
];

const SLOTS = SLOT_NAMES.map((_, i) => i);

const slot = (a: Assignment, task: string): number | undefined => {
  const v = a[task];
  return typeof v === "number" ? v : undefined;
};

export function buildSchedulingSolver(): MaxCSPSolver {
  const solver = new MaxCSPSolver();

  for (const task of TASKS) {
    solver.addVariable(task.id, SLOTS);
  }

  // ---- 硬约束 ----
  solver.addHardConstraint({
    name: "H1",
    description: "资源独占：同一时段最多安排一个任务",
    consistent(a) {
      const used = new Set<number>();
      for (const t of TASKS) {
        const s = slot(a, t.id);
        if (s === undefined) continue;
        if (used.has(s)) return false;
        used.add(s);
      }
      return true;
    },
  });

  solver.addHardConstraint({
    name: "H2",
    description: "硬性截止：需求评审必须安排在周三（含）之前",
    consistent(a) {
      const s = slot(a, "T1");
      return s === undefined || s <= 2;
    },
  });

  solver.addHardConstraint({
    name: "H3",
    description: "合规前置：宣传物料必须早于版本上线",
    consistent(a) {
      const material = slot(a, "T4");
      const release = slot(a, "T3");
      if (material === undefined || release === undefined) return true;
      return material < release;
    },
  });

  // ---- 软约束 ----
  const preferSlot = (task: string, target: number) =>
    (a: Assignment): boolean | null => {
      const s = slot(a, task);
      return s === undefined ? null : s !== target;
    };

  solver.addSoftConstraint({
    name: "S1",
    weight: 10,
    description: "需求评审排在周一（关键干系人仅周一有空）",
    violated: preferSlot("T1", 0),
  });

  solver.addSoftConstraint({
    name: "S2",
    weight: 8,
    description: "原型设计排在周二（设计团队冲刺节奏）",
    violated: preferSlot("T2", 1),
  });

  solver.addSoftConstraint({
    name: "S3",
    weight: 2,
    description: "版本上线排在周三（次要：发布日偏好）",
    violated: preferSlot("T3", 2),
  });

  solver.addSoftConstraint({
    name: "S4",
    weight: 4,
    description: "宣传物料排在周四（市场活动节点）",
    violated: preferSlot("T4", 3),
  });

  solver.addSoftConstraint({
    name: "S5",
    weight: 1,
    description: "备份演练排在版本上线后第 2 天（次要：运维偏好）",
    violated(a) {
      const drill = slot(a, "T5");
      const release = slot(a, "T3");
      if (drill === undefined || release === undefined) return null;
      return drill !== release + 2;
    },
  });

  return solver;
}

const taskLabel = (id: string): string => TASKS.find((t) => t.id === id)!.label;

/** 终端显示宽度：ASCII 计 1，其余（CJK 等全角字符）计 2。 */
const displayWidth = (s: string): number =>
  [...s].reduce((w, ch) => w + (ch.codePointAt(0)! > 0xff ? 2 : 1), 0);

const padTo = (s: string, width: number): string =>
  s + " ".repeat(Math.max(0, width - displayWidth(s)));

/** 把解渲染成按时段排序的排期表。 */
export function formatSchedule(solution: Solution): string {
  const bySlot = new Map<number, string>();
  for (const t of TASKS) {
    bySlot.set(solution.assignment[t.id] as number, t.id);
  }

  const slotCol = 6;
  const taskCol = 14;
  const border = (left: string, mid: string, right: string, fill: string): string =>
    left + fill.repeat(slotCol + 2) + mid + fill.repeat(taskCol + 2) + right;

  const lines: string[] = [];
  lines.push(border("┌", "┬", "┐", "─"));
  lines.push(`│ ${padTo("时段", slotCol)} │ ${padTo("任务", taskCol)} │`);
  lines.push(border("├", "┼", "┤", "─"));
  for (let s = 0; s < SLOT_NAMES.length; s++) {
    const taskId = bySlot.get(s);
    const label = taskId ? `${taskId} ${taskLabel(taskId)}` : "(空)";
    lines.push(`│ ${padTo(SLOT_NAMES[s]!, slotCol)} │ ${padTo(label, taskCol)} │`);
  }
  lines.push(border("└", "┴", "┘", "─"));
  return lines.join("\n");
}
