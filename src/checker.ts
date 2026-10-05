// 编排：解析 -> chase（无损性） -> 依赖保持。两项判定独立进行、独立报告。
import { chase } from "./chase.ts";
import { checkPreservation } from "./preservation.ts";
import type { CheckReport, Spec } from "./types.ts";

export function check(spec: Spec): CheckReport {
  const chaseResult = chase(spec);
  const preservation = checkPreservation(spec);
  return {
    spec,
    chase: chaseResult,
    preservation,
    dependenciesPreserved: preservation.every((p) => p.preserved),
  };
}
