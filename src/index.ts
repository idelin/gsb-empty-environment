/**
 * 边缘网关有状态过滤策略的热切换变更检查包。
 *
 * 用法：
 *   import { checkChange, verifyCertificate } from "./src/index.ts";
 *   const result = checkChange(oldPolicy, newPolicy);
 *   // result.verdict: "pass" | "fail"
 *   // result.violations: 违反包含关系的规则编号等
 *   // result.certificate: 通过时的不扩大证明，可用
 *   //   verifyCertificate(result.certificate, oldPolicy, newPolicy)
 *   // 独立复核。
 *
 * 模型假设：单条连接的双向报文；变更期间端点、端口与传输协议不变；
 * 每份策略至多 40 条规则；标签为 3 位（0..7），首次标签为 0。
 */

export {
  MAX_RULES,
  LABEL_BITS,
  LABEL_COUNT,
  MAX_PORT,
  MAX_PREFIX_LEN,
  compilePolicy,
  evalPolicy,
  intToIpv4,
  ipv4ToInt,
  matchCondition,
  prefixMask,
  validatePolicy,
} from "./model.ts";
export type {
  Action,
  CompiledPolicy,
  Condition,
  Direction,
  EvalResult,
  Packet,
  Policy,
  Rule,
  Side,
} from "./model.ts";

export { computeClasses, packetFor } from "./classes.ts";
export type { ClassInfo } from "./classes.ts";

export { reachablePairs } from "./reachability.ts";
export type { PairReach, StepOutcome, StepRecord } from "./reachability.ts";

export {
  CERTIFICATE_VERSION,
  findViolations,
  verifyCertificate,
} from "./certificate.ts";
export type {
  CertClass,
  CertPair,
  CertStep,
  CertViolation,
  Certificate,
  VerifyResult,
} from "./certificate.ts";

export { checkChange } from "./check.ts";
export type { CheckResult, CheckStats } from "./check.ts";
