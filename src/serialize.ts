import { createHash } from 'node:crypto';
import { RestoreError } from './errors.ts';
import type { Registry } from './registry.ts';
import {
  checkEvaluationCycles,
  checkMissingDeps,
  checkMonotonicCycles,
  checkSharedIdentity,
  recomputeAll,
  type RecordMap,
} from './graph.ts';
import type { Store } from './store.ts';

/** 快照格式版本；不兼容即拒绝恢复。 */
export const SCHEMA_VERSION = 1;

export type NodeFile =
  | { readonly id: string; readonly kind: 'state'; readonly value: unknown }
  | {
      readonly id: string;
      readonly kind: 'computed';
      readonly def: { readonly id: string; readonly version: number };
      readonly deps: readonly string[];
      /** 单调计算节点保存的上一求值值（函数本体绝不落盘）。 */
      readonly carried?: unknown;
    };

export interface SnapshotFile {
  readonly schemaVersion: number;
  readonly nodes: readonly NodeFile[];
  /** 可选完整性摘要（sha256），存在则恢复时强制校验。 */
  readonly integrity?: string;
}

/** 确定性序列化（对象键排序），用于完整性摘要。 */
export function stableStringify(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value) ?? 'undefined';
  if (Array.isArray(value)) return `[${value.map(stableStringify).join(',')}]`;
  const obj = value as Record<string, unknown>;
  const keys = Object.keys(obj).sort();
  return `{${keys.map((k) => `${JSON.stringify(k)}:${stableStringify(obj[k])}`).join(',')}}`;
}

function digestPayload(schemaVersion: number, nodes: readonly NodeFile[]): string {
  return createHash('sha256')
    .update(stableStringify({ schemaVersion, nodes }))
    .digest('hex');
}

/** 序列化：只写节点标识、状态值、定义标识+版本、依赖边与单调节点的携带值。 */
export function serializeStore(store: Store, opts?: { integrity?: boolean }): SnapshotFile {
  const nodes: NodeFile[] = [];
  for (const rec of store.recordsView().values() as Iterable<{
    kind: string;
    id: string;
    value?: unknown;
    defId?: string;
    defVersion?: number;
    deps?: readonly string[];
  }>) {
    if (rec.kind === 'state') {
      nodes.push({ id: rec.id, kind: 'state', value: rec.value });
    } else {
      nodes.push({
        id: rec.id,
        kind: 'computed',
        def: { id: rec.defId as string, version: rec.defVersion as number },
        deps: (rec.deps as readonly string[]).slice(),
        carried: rec.value,
      });
    }
  }
  const file: SnapshotFile = { schemaVersion: SCHEMA_VERSION, nodes };
  if (opts?.integrity) {
    return { ...file, integrity: digestPayload(file.schemaVersion, file.nodes) };
  }
  return file;
}

function assertNodeShape(node: unknown): asserts node is NodeFile {
  const n = node as NodeFile & { def?: { id?: unknown; version?: unknown } };
  if (!n || typeof n !== 'object' || typeof n.id !== 'string') {
    throw new RestoreError('MISSING_NODE', 'snapshot node missing string id');
  }
  if (n.kind === 'state') return;
  if (n.kind === 'computed') {
    const def = (n as { def?: { id?: unknown; version?: unknown } }).def;
    if (!def || typeof def.id !== 'string' || typeof def.version !== 'number') {
      throw new RestoreError('UNKNOWN_DEFINITION', `computed node ${n.id} has malformed def reference`);
    }
    if (!Array.isArray((n as { deps?: unknown }).deps)) {
      throw new RestoreError('MISSING_NODE', `computed node ${n.id} has malformed deps`);
    }
    return;
  }
  throw new RestoreError('MISSING_NODE', `unknown node kind for id: ${n.id}`);
}

/**
 * 暂存恢复：完成全部校验与求值后返回新记录表。
 * 不触碰任何 live store —— 失败时旧状态保持可读。
 *
 * 顺序：schema 版本 → 完整性 → 共享身份 → 纯状态落位 →
 *       定义标识/版本解析 → 缺节点 → 单调环 → 非法求值环 → 重算。
 */
export function stageRestore(file: unknown, registry: Registry): RecordMap {
  const f = file as SnapshotFile;
  if (!f || typeof f !== 'object') {
    throw new RestoreError('SCHEMA_VERSION', 'snapshot is not an object');
  }
  if (f.schemaVersion !== SCHEMA_VERSION) {
    throw new RestoreError(
      'SCHEMA_VERSION',
      `unsupported schemaVersion: ${String(f.schemaVersion)} (expected ${SCHEMA_VERSION})`,
    );
  }
  if (!Array.isArray(f.nodes)) {
    throw new RestoreError('MISSING_NODE', 'snapshot.nodes must be an array');
  }
  if (f.integrity !== undefined && f.integrity !== digestPayload(f.schemaVersion, f.nodes)) {
    throw new RestoreError('INTEGRITY', 'snapshot integrity digest mismatch (tampered payload)');
  }
  for (const node of f.nodes) assertNodeShape(node);
  checkSharedIdentity(f.nodes.map((n) => n.id));

  const records: RecordMap = new Map();

  // 阶段一：只恢复纯状态节点。
  for (const node of f.nodes) {
    if (node.kind === 'state') {
      records.set(node.id, { kind: 'state', id: node.id, value: node.value });
    }
  }

  // 阶段二：按保存的依赖证据重建计算图（绝不执行快照中的代码，只查注册表）。
  for (const node of f.nodes) {
    if (node.kind !== 'computed') continue;
    const def = registry.get(node.def.id, node.def.version);
    if (!def) {
      if (registry.hasId(node.def.id)) {
        throw new RestoreError(
          'DEFINITION_VERSION',
          `definition ${node.def.id} version ${node.def.version} not registered ` +
            `(available: [${registry.versionsOf(node.def.id)}])`,
        );
      }
      throw new RestoreError('UNKNOWN_DEFINITION', `unknown definition: ${node.def.id}@${node.def.version}`);
    }
    // 单调节点的携带值即恢复后的不动点，直接落位、不再重算；
    // 其余计算节点一律按依赖证据重算。
    const settled = def.monotonic === true && node.carried !== undefined;
    records.set(node.id, {
      kind: 'computed',
      id: node.id,
      defId: node.def.id,
      defVersion: node.def.version,
      deps: node.deps.slice(),
      carried: node.carried,
      value: settled ? node.carried : undefined,
      dirty: !settled,
    });
  }

  checkMissingDeps(records);
  checkMonotonicCycles(records, registry);
  checkEvaluationCycles(records);
  recomputeAll(records, new Map(), registry);
  return records;
}
