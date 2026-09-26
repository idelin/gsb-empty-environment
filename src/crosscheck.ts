/**
 * 交叉核验：执行器跑出的轨迹，逐步用检查器的一步语义重算并比对。
 */

import { step } from "./checker.ts";
import { Executor, hashConfig } from "./executor.ts";
import { compile, configEquals, type Config, type Ev, type Spec } from "./spec.ts";

export interface CrosscheckMismatch {
  seq: number;
  event: Ev;
  executorConfig: Config;
  checkerConfig: Config | null;
  checkerReason: string;
}

export interface CrosscheckReport {
  steps: number;
  mismatches: CrosscheckMismatch[];
}

/**
 * 对每条“已应用”的日志条目：取执行前配置，用检查器 step 独立重算，
 * 与执行器结果比对。任何不一致都说明两套语义出现分歧。
 */
export function crossValidate(spec: Spec, events: Ev[]): CrosscheckReport {
  const compiled = compile(spec);
  const executor = new Executor(spec);
  const mismatches: CrosscheckMismatch[] = [];

  let prevConfig = executor.getConfig();
  let steps = 0;
  for (const event of events) {
    const result = executor.dispatch(event);
    if (result.status === "applied") {
      steps++;
      const independent = step(compiled, prevConfig, event);
      const executorNext = result.config;
      const ok =
        independent.next !== null &&
        configEquals(independent.next, executorNext) &&
        hashConfig(independent.next) === hashConfig(executorNext);
      if (!ok) {
        mismatches.push({
          seq: steps,
          event,
          executorConfig: executorNext,
          checkerConfig: independent.next,
          checkerReason: independent.reason,
        });
      }
    } else {
      // 执行器拒绝/回滚时，检查器的一步语义也应给出非 ok 结论或相同结果。
      const independent = step(compiled, prevConfig, event);
      if (result.status === "rejected" && independent.reason === "ok") {
        mismatches.push({
          seq: steps,
          event,
          executorConfig: prevConfig,
          checkerConfig: independent.next,
          checkerReason: `executor rejected but checker says ok`,
        });
      }
    }
    prevConfig = executor.getConfig();
  }
  return { steps, mismatches };
}
