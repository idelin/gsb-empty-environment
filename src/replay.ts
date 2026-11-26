/**
 * 无缓存全事件重放核对。
 *
 * - replayAll：从零开始重放完整事件流，不读取任何外部缓存状态。
 * - replayPermutations：对事件流做确定性乱序排列重放，断言最终状态收敛一致。
 * - replayFromCheckpoint：从检查点快照继续重放，并强制哈希核对。
 */

import { OwnershipEngine, hashObjectState } from './engine.ts';
import { hashCanonical } from './canonical.ts';
import type {
  CheckpointSnapshot,
  DomainEvent,
  Evidence,
  IngestResult,
  ObjectId,
  ObjectState,
  OwnershipState,
} from './types.ts';

export interface ReplayReport {
  state: OwnershipState;
  results: IngestResult[];
  objectHashes: Record<ObjectId, string>;
  stateHash: string;
  pendingCount: number;
  rejectedCount: number;
}

function snapshotObjectHashes(engine: OwnershipEngine): Record<ObjectId, string> {
  const hashes: Record<ObjectId, string> = {};
  for (const objectId of Object.keys(engine.state.objects).sort()) {
    hashes[objectId] = hashObjectState(engine.state.objects[objectId]);
  }
  return hashes;
}

function buildReport(engine: OwnershipEngine, results: IngestResult[]): ReplayReport {
  const objectHashes = snapshotObjectHashes(engine);
  return {
    state: engine.state,
    results,
    objectHashes,
    stateHash: hashCanonical(objectHashes),
    pendingCount: engine.state.pending.length,
    rejectedCount: Object.keys(engine.state.rejected).length,
  };
}

/** 从零无缓存重放完整事件流。 */
export function replayAll(events: Iterable<DomainEvent>): ReplayReport {
  const engine = new OwnershipEngine();
  const results = engine.ingestAll(events);
  results.push(...engine.settle());
  return buildReport(engine, results);
}

/**
 * 确定性乱序重放：按给定排列（或内置的多轮排列）重放同一事件流，
 * 返回每轮报告，调用方可直接逐对象比对哈希以验证收敛语义。
 */
export function replayPermutations(
  events: ReadonlyArray<DomainEvent>,
  permutations?: ReadonlyArray<ReadonlyArray<number>>,
): ReplayReport[] {
  const plans = permutations ?? builtinPermutations(events.length);
  return plans.map((order) => {
    if (order.length !== events.length || new Set(order).size !== events.length) {
      throw new Error('乱序排列必须覆盖全部事件且不重复');
    }
    const shuffled = order.map((index) => events[index]);
    return replayAll(shuffled);
  });
}

/** 内置排列：逆序、轮转、按 eventId 哈希分桶交错，均为确定性排列。 */
export function builtinPermutations(length: number): Array<Array<number>> {
  const identity = Array.from({ length }, (_, index) => index);
  const reversed = [...identity].reverse();
  const rotated = identity.map((index) => (index + Math.floor(length / 2)) % length);
  const interleaved = identity
    .filter((index) => index % 2 === 0)
    .concat(identity.filter((index) => index % 2 === 1));
  return [identity, reversed, rotated, interleaved];
}

/** 断言多轮重放逐对象哈希完全一致（收敛性的可执行证据）。 */
export function assertConvergent(reports: ReadonlyArray<ReplayReport>): void {
  if (reports.length < 2) {
    return;
  }
  const baseline = reports[0].stateHash;
  for (let index = 1; index < reports.length; index += 1) {
    if (reports[index].stateHash !== baseline) {
      const diffs = Object.keys({ ...reports[0].objectHashes, ...reports[index].objectHashes })
        .sort()
        .filter(
          (objectId) => reports[0].objectHashes[objectId] !== reports[index].objectHashes[objectId],
        );
      throw new Error(`乱序重放未收敛，分歧对象: ${diffs.join(', ') || '未知'}`);
    }
    if (reports[index].pendingCount !== reports[0].pendingCount) {
      throw new Error('乱序重放后挂起事件数量不一致');
    }
  }
}

/** 从检查点快照恢复引擎并继续重放，核对快照哈希后再处理后续事件。 */
export function replayFromCheckpoint(
  snapshot: CheckpointSnapshot,
  subsequentEvents: Iterable<DomainEvent>,
): ReplayReport {
  const engine = new OwnershipEngine();
  restoreSnapshot(engine, snapshot);
  if (hashObjectState(engine.state.objects[snapshot.objectId]) !== snapshot.stateHash) {
    throw new Error('检查点快照自哈希不一致，拒绝从该压缩点恢复');
  }
  const results = engine.ingestAll(subsequentEvents);
  results.push(...engine.settle());
  return buildReport(engine, results);
}

/** 比较两份对象状态的哈希（用于全量重放 vs 检查点恢复的等价核对）。 */
export function statesEqual(left: ObjectState | undefined, right: ObjectState | undefined): boolean {
  if (left === undefined || right === undefined) {
    return left === right;
  }
  return hashObjectState(left) === hashObjectState(right);
}

/** 收集报告内全部证据（按事件应用顺序）。 */
export function collectEvidence(report: ReplayReport): Evidence[] {
  return report.results.flatMap((result) => result.evidence);
}

function restoreSnapshot(engine: OwnershipEngine, snapshot: CheckpointSnapshot): void {
  const restored: ObjectState = structuredClone(snapshot.state);
  restored.spentNonces = reviveSet(restored.spentNonces);
  restored.seenEventIds = reviveSet(restored.seenEventIds);
  restored.transportNonces = Object.fromEntries(
    Object.entries(restored.transportNonces).map(([relay, set]) => [relay, reviveSet(set as ReadonlySet<string>)]),
  );
  engine.state.objects[snapshot.objectId] = restored;
  engine.state.appliedSeq = snapshot.throughAppliedSeq;
  engine.state.checkpoints[snapshot.objectId] = {
    throughAppliedSeq: snapshot.throughAppliedSeq,
    stateHash: snapshot.stateHash,
  };
}

function reviveSet<T>(value: ReadonlySet<T> | Readonly<Record<string, T>>): Set<T> {
  if (value instanceof Set) {
    return new Set(value);
  }
  if (Array.isArray(value)) {
    return new Set(value);
  }
  // JSON 往返后 Set 退化为 { [元素]: true } 形式，键才是原集合元素。
  return new Set(Object.keys(value) as T[]);
}
