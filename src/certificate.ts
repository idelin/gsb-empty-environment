import { createHash } from 'node:crypto';
import type { ErrorNode } from './types.ts';
import type { SchemaRegistry } from './registry.ts';
import {
  allowAllPolicy,
  buildProjectionPlan,
  renderPlanFrames,
  type ProjectionResult,
} from './project.ts';

/** 后台完整诊断记录：包含全部节点与字段，仅供内部保存。 */
export interface DiagnosticRecord {
  registryVersion: number;
  fullText: string;
}

export function recordDiagnostic(root: ErrorNode, registry: SchemaRegistry): DiagnosticRecord {
  const plan = buildProjectionPlan(root, registry, allowAllPolicy);
  return {
    registryVersion: registry.version,
    fullText: `${renderPlanFrames(plan, registry.version).join('\n')}\n`,
  };
}

/** 公开证书：仅含获准字节及其摘要，不携带任何隐藏子图信息。 */
export interface PublicCertificate {
  registryVersion: number;
  byteLength: number;
  sha256: string;
  bytes: Uint8Array;
}

export function publicCertificate(
  result: ProjectionResult,
  registry: SchemaRegistry,
): PublicCertificate {
  return {
    registryVersion: registry.version,
    byteLength: result.bytes.length,
    sha256: createHash('sha256').update(result.bytes).digest('hex'),
    bytes: result.bytes,
  };
}
