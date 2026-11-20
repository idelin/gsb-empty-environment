/**
 * 不扩大证明（证书）。
 *
 * 检查通过时产出的证书是纯数据（可 JSON 序列化），包含：
 *  - 全部抽象类别（地址/端口等价类的区间与代表点）；
 *  - 每个类别中全部可达标签对及从 (0,0) 出发的见证轨迹；
 *  - 违反包含关系的（类别, 标签对, 规则）列表（通过时为空）。
 *
 * verifyCertificate 只依赖证书与两份策略，重新计算类别划分、
 * 可达标签对与违规集合，并逐项回放轨迹，从而独立确认：
 * 证书覆盖了所有前缀、端口与标签等价类，且结论正确。
 */

import { computeClasses, packetFor } from "./classes.ts";
import type { ClassInfo } from "./classes.ts";
import { LABEL_COUNT, compilePolicy, evalPolicy } from "./model.ts";
import type { CompiledPolicy, Direction, Policy } from "./model.ts";
import { reachablePairs } from "./reachability.ts";
import type { PairReach, StepRecord } from "./reachability.ts";

export const CERTIFICATE_VERSION = 1;

export interface CertClass {
  id: number;
  src: [number, number];
  dst: [number, number];
  srcPort: [number, number];
  dstPort: [number, number];
  rep: { src: number; dst: number; srcPort: number; dstPort: number };
}

export interface CertStep {
  direction: Direction;
  before: [number, number];
  oldOutcome: { rule: number; allow: boolean; label: number };
  newOutcome: { rule: number; allow: boolean; label: number };
  after: [number, number];
}

export interface CertPair {
  classId: number;
  oldLabel: number;
  newLabel: number;
  trace: CertStep[];
}

export interface CertViolation {
  classId: number;
  oldLabel: number;
  newLabel: number;
  /** 权限扩大发生的报文方向。 */
  direction: Direction;
  /** 新策略中导致权限扩大的规则声明序号。 */
  newRule: number;
  /** 旧策略在同一标签下拒绝该报文的规则序号；-1 表示默认拒绝。 */
  oldRule: number;
}

export interface Certificate {
  version: number;
  classes: CertClass[];
  pairs: CertPair[];
  violations: CertViolation[];
}

export function classToCert(cls: ClassInfo): CertClass {
  return {
    id: cls.id,
    src: [cls.srcLo, cls.srcHi],
    dst: [cls.dstLo, cls.dstHi],
    srcPort: [cls.srcPortLo, cls.srcPortHi],
    dstPort: [cls.dstPortLo, cls.dstPortHi],
    rep: { ...cls.rep },
  };
}

export function stepToCert(step: StepRecord): CertStep {
  return {
    direction: step.direction,
    before: [step.before[0], step.before[1]],
    oldOutcome: { ...step.oldOutcome },
    newOutcome: { ...step.newOutcome },
    after: [step.after[0], step.after[1]],
  };
}

export function pairToCert(classId: number, pair: PairReach): CertPair {
  return {
    classId,
    oldLabel: pair.oldLabel,
    newLabel: pair.newLabel,
    trace: pair.trace.map(stepToCert),
  };
}

function pairKey(classId: number, oldLabel: number, newLabel: number): string {
  return `${classId}:${oldLabel}:${newLabel}`;
}

function violationKey(v: CertViolation): string {
  return `${v.classId}:${v.oldLabel}:${v.newLabel}:${v.direction}:${v.newRule}:${v.oldRule}`;
}

/**
 * 在类别代表点上重算一对标签的授权包含关系，返回全部违规。
 * 两个方向都要核对：方向是报文属性，无法通过对称类别归约。
 */
export function findViolations(
  oldPolicy: CompiledPolicy,
  newPolicy: CompiledPolicy,
  cls: ClassInfo,
  oldLabel: number,
  newLabel: number,
): CertViolation[] {
  const out: CertViolation[] = [];
  const directions: Direction[] = ["forward", "reverse"];
  for (const direction of directions) {
    const pkt = packetFor(cls, direction);
    const oldOutcome = evalPolicy(oldPolicy, pkt, oldLabel);
    const newOutcome = evalPolicy(newPolicy, pkt, newLabel);
    if (!oldOutcome.allow && newOutcome.allow) {
      out.push({
        classId: cls.id,
        oldLabel,
        newLabel,
        direction,
        newRule: newOutcome.rule,
        oldRule: oldOutcome.rule,
      });
    }
  }
  return out;
}

export interface VerifyResult {
  ok: boolean;
  errors: string[];
}

/**
 * 独立验证证书：重新计算类别、可达标签对与违规集合，
 * 并回放每条见证轨迹。任何不一致都会使验证失败。
 */
export function verifyCertificate(
  cert: Certificate,
  oldPolicy: Policy,
  newPolicy: Policy,
): VerifyResult {
  const errors: string[] = [];
  if (cert.version !== CERTIFICATE_VERSION) {
    errors.push(`证书版本 ${cert.version} 不受支持`);
    return { ok: false, errors };
  }

  let oldCompiled: CompiledPolicy;
  let newCompiled: CompiledPolicy;
  try {
    oldCompiled = compilePolicy(oldPolicy, "oldPolicy");
    newCompiled = compilePolicy(newPolicy, "newPolicy");
  } catch (err) {
    return { ok: false, errors: [`策略不合法: ${(err as Error).message}`] };
  }

  // 1. 类别覆盖：证书列出的类别必须与重新计算的完全一致。
  const classes = computeClasses(oldPolicy, newPolicy);
  if (cert.classes.length !== classes.length) {
    errors.push(`类别数量不符: 证书 ${cert.classes.length}，重算 ${classes.length}`);
  }
  const n = Math.min(cert.classes.length, classes.length);
  for (let i = 0; i < n; i++) {
    const got = cert.classes[i];
    const want = classToCert(classes[i]);
    if (JSON.stringify(got) !== JSON.stringify(want)) {
      errors.push(`类别 ${i} 不一致: 证书 ${JSON.stringify(got)}，重算 ${JSON.stringify(want)}`);
    }
  }

  // 2. 标签对覆盖：证书列出的可达标签对必须与重新计算的完全一致。
  const expectedPairs = new Map<string, { cls: ClassInfo; pair: PairReach }>();
  for (const cls of classes) {
    for (const pair of reachablePairs(oldCompiled, newCompiled, cls)) {
      expectedPairs.set(pairKey(cls.id, pair.oldLabel, pair.newLabel), { cls, pair });
    }
  }
  const seen = new Set<string>();
  for (const pair of cert.pairs) {
    const k = pairKey(pair.classId, pair.oldLabel, pair.newLabel);
    if (seen.has(k)) errors.push(`标签对重复: ${k}`);
    seen.add(k);
    if (!expectedPairs.has(k)) errors.push(`证书包含不可达标签对: ${k}`);
  }
  for (const k of expectedPairs.keys()) {
    if (!seen.has(k)) errors.push(`证书缺少可达标签对: ${k}`);
  }

  // 3. 轨迹回放：逐步重算两份策略的求值结果并与证书比对。
  for (const pair of cert.pairs) {
    const cls = classes[pair.classId];
    if (!cls || cls.id !== pair.classId) {
      errors.push(`标签对 ${pairKey(pair.classId, pair.oldLabel, pair.newLabel)} 引用未知类别`);
      continue;
    }
    let state: [number, number] = [0, 0];
    pair.trace.forEach((step, index) => {
      const where = `类别 ${pair.classId} 轨迹第 ${index} 步`;
      if (step.before[0] !== state[0] || step.before[1] !== state[1]) {
        errors.push(`${where}: 起始标签对 ${step.before} 与上一步结果 ${state} 不符`);
      }
      const pkt = packetFor(cls, step.direction);
      const oldOutcome = evalPolicy(oldCompiled, pkt, state[0]);
      const newOutcome = evalPolicy(newCompiled, pkt, state[1]);
      if (JSON.stringify(step.oldOutcome) !== JSON.stringify(oldOutcome)) {
        errors.push(`${where}: 旧策略结果不符`);
      }
      if (JSON.stringify(step.newOutcome) !== JSON.stringify(newOutcome)) {
        errors.push(`${where}: 新策略结果不符`);
      }
      const after: [number, number] = [
        oldOutcome.allow ? oldOutcome.label : state[0],
        newOutcome.allow ? newOutcome.label : state[1],
      ];
      if (step.after[0] !== after[0] || step.after[1] !== after[1]) {
        errors.push(`${where}: 结束标签对 ${step.after} 与重算 ${after} 不符`);
      }
      state = after;
    });
    if (state[0] !== pair.oldLabel || state[1] !== pair.newLabel) {
      errors.push(
        `类别 ${pair.classId}: 轨迹终点 ${state} 与声明的标签对 ` +
          `(${pair.oldLabel}, ${pair.newLabel}) 不符`,
      );
    }
    if (
      pair.oldLabel < 0 ||
      pair.oldLabel >= LABEL_COUNT ||
      pair.newLabel < 0 ||
      pair.newLabel >= LABEL_COUNT
    ) {
      errors.push(`类别 ${pair.classId}: 标签越界`);
    }
  }

  // 4. 违规集合：按重算的标签对重新核对授权包含关系。
  const expectedViolations: CertViolation[] = [];
  for (const { cls, pair } of expectedPairs.values()) {
    expectedViolations.push(
      ...findViolations(oldCompiled, newCompiled, cls, pair.oldLabel, pair.newLabel),
    );
  }
  const certViolationKeys = new Set(cert.violations.map(violationKey));
  const expectedKeys = new Set(expectedViolations.map(violationKey));
  for (const k of certViolationKeys) {
    if (!expectedKeys.has(k)) errors.push(`证书声明了不存在的违规: ${k}`);
  }
  for (const k of expectedKeys) {
    if (!certViolationKeys.has(k)) errors.push(`证书遗漏违规: ${k}`);
  }

  return { ok: errors.length === 0, errors };
}
