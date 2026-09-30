import { canonical, type JSONValue } from './json.ts';
import { compilePlan, type Analysis, type DepGraph, type StepDerivation } from './infer.ts';
import type { Step } from './plan.ts';
import { typeKey, type Type } from './types.ts';

/**
 * 可重放的类型推导证书：完整记录每步的规则、读写、输入哈希与输出模式。
 * 证书为纯 JSON，可序列化、可离线重放校验。
 */
export interface Certificate {
  version: 1;
  planHash: string;
  inputHash: string;
  finalSchema: Type;
  steps: StepDerivation[];
  depGraph: DepGraph;
  diagnostics: Analysis['diagnostics'];
}

export function issueCertificate(
  steps: Step[],
  inputSchema: Type,
): { analysis: Analysis; certificate: Certificate } {
  const analysis = compilePlan(steps, inputSchema);
  const certificate: Certificate = {
    version: 1,
    planHash: canonical(steps as unknown as JSONValue),
    inputHash: typeKey(inputSchema),
    finalSchema: analysis.schema,
    steps: analysis.derivations,
    depGraph: analysis.depGraph,
    diagnostics: analysis.diagnostics,
  };
  return { analysis, certificate };
}

export interface ReplayResult {
  ok: boolean;
  mismatches: string[];
}

/** 重放证书：重新推导并逐字段比对，报告不一致位置。 */
export function replayCertificate(
  steps: Step[],
  inputSchema: Type,
  cert: Certificate,
): ReplayResult {
  const { certificate: fresh } = issueCertificate(steps, inputSchema);
  const mismatches: string[] = [];
  const cmp = (label: string, a: unknown, b: unknown) => {
    if (canonical(a as JSONValue) !== canonical(b as JSONValue)) mismatches.push(label);
  };
  cmp('planHash', cert.planHash, fresh.planHash);
  cmp('inputHash', cert.inputHash, fresh.inputHash);
  cmp('finalSchema', cert.finalSchema as unknown as JSONValue, fresh.finalSchema as unknown as JSONValue);
  if (cert.steps.length !== fresh.steps.length) {
    mismatches.push(`steps.length (${cert.steps.length} != ${fresh.steps.length})`);
  } else {
    cert.steps.forEach((s, i) => {
      cmp(`steps[${i}](${s.stepId})`, s as unknown as JSONValue, fresh.steps[i] as unknown as JSONValue);
    });
  }
  cmp('depGraph', cert.depGraph as unknown as JSONValue, fresh.depGraph as unknown as JSONValue);
  cmp('diagnostics', cert.diagnostics as unknown as JSONValue, fresh.diagnostics as unknown as JSONValue);
  return { ok: mismatches.length === 0, mismatches };
}
