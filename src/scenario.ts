/**
 * 验收场景构造：跨 2 个分区的并发事务 T1/T2 交错日志，
 * 以及快照切片点之后的后续事务日志。
 */

import type { LogEvent } from './types.ts';

export const PARTITION_COUNT = 2;

/** 快照切片点：前 SNAPSHOT_AT 个事件回放完后生成快照。 */
export const SNAPSHOT_AT = 6;

export function buildLog(): LogEvent[] {
  const ev = (
    seq: number,
    txnId: string,
    kind: LogEvent['kind'],
    partitions: number[],
    reads: Record<number, string[]> = {},
    writes: Record<number, Record<string, string>> = {},
  ): LogEvent => ({ seq, txnId, kind, partitions, reads, writes });

  return [
    // T1 与 T2 并发交错，且在 P0:shared 上写-写冲突、在 P1:x 上读-写冲突。
    ev(0, 'T1', 'prepare', [0, 1], { 0: ['shared'], 1: ['x'] },
      { 0: { shared: 'T1', a: '1' }, 1: { x: '1' } }),
    ev(1, 'T2', 'prepare', [0, 1], { 1: ['x', 'y'] },
      { 0: { shared: 'T2', b: '2' }, 1: { y: '2' } }),
    ev(2, 'T1', 'commit', [0, 1]),   // T1 先提交 => T2 被决胜中止
    ev(3, 'T2', 'commit', [0, 1]),   // 幂等空操作（T2 已中止）
    // T3 与 T1 无冲突，正常提交。
    ev(4, 'T3', 'prepare', [1], {}, { 1: { z: '3' } }),
    ev(5, 'T3', 'commit', [1]),
    // ---- 快照切片点（seq 6 之前）----
    // T4 与未决的 T5 冲突，T5 先 commit => T4 被决胜中止。
    ev(6, 'T4', 'prepare', [0], {}, { 0: { hot: 'T4' } }),
    ev(7, 'T5', 'prepare', [0, 1], {}, { 0: { hot: 'T5' }, 1: { w: '5' } }),
    ev(8, 'T5', 'commit', [0, 1]),
    ev(9, 'T4', 'commit', [0]),
    // T6 显式中止，不产生任何写。
    ev(10, 'T6', 'prepare', [1], {}, { 1: { ghost: 'T6' } }),
    ev(11, 'T6', 'abort', [1]),
  ];
}

/** 确定性伪随机（mulberry32），用于可复现的乱序注入。 */
export function mulberry32(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** 用种子对事件做确定性洗牌，模拟乱序网络包（含一次重复投递）。 */
export function shuffledDelivery(events: LogEvent[], seed: number): LogEvent[] {
  const rand = mulberry32(seed);
  const out = [...events];
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  // 注入一个重复包，验证幂等。
  out.splice(Math.floor(rand() * out.length), 0, out[0]);
  return out;
}
