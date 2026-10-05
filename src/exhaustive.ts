// 全输入穷举:对每个输入向量做具体执行,作为符号执行结果的基准。

import { INT8_MIN, INT8_MAX, runConcrete, type Program } from "./lang.ts";

export function forEachInput(numInputs: number, fn: (vec: number[]) => void): void {
  const vec = new Array<number>(numInputs).fill(INT8_MIN);
  const rec = (i: number): void => {
    if (i === numInputs) {
      fn(vec);
      return;
    }
    for (let v = INT8_MIN; v <= INT8_MAX; v++) {
      vec[i] = v;
      rec(i + 1);
    }
  };
  rec(0);
}

export const inputKey = (vec: number[]): string => vec.join(",");

export type ExhaustiveResult = {
  runs: number;
  failSet: Map<number, Set<string>>;  // assertId -> 触发失败的输入集合
  minFailing: Map<number, number[]>;  // assertId -> 字典序最小失败输入
  overflowSet: Set<string>;           // 发生溢出的输入
  boundSet: Set<string>;              // 循环超界被截断的输入
  edges: Set<string>;                 // 所有被执行到的分支边
};

export function runExhaustive(prog: Program): ExhaustiveResult {
  const result: ExhaustiveResult = {
    runs: 0,
    failSet: new Map(),
    minFailing: new Map(),
    overflowSet: new Set(),
    boundSet: new Set(),
    edges: new Set(),
  };
  forEachInput(prog.inputs, (vec) => {
    result.runs++;
    const r = runConcrete(prog, vec);
    const key = inputKey(vec);
    if (r.overflow) result.overflowSet.add(key);
    if (r.status === "bound-exceeded") result.boundSet.add(key);
    if (r.status === "assert-failed" && r.failedAssert !== null) {
      const id = r.failedAssert;
      let set = result.failSet.get(id);
      if (!set) {
        set = new Set();
        result.failSet.set(id, set);
      }
      set.add(key);
      // 枚举顺序即字典序,首个失败输入即最小输入
      if (!result.minFailing.has(id)) result.minFailing.set(id, [...vec]);
    }
    for (const e of r.edges) result.edges.add(e);
  });
  return result;
}
