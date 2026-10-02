import type { Cell } from './tombstone.ts';

/**
 * 一条 LSM 记录。seq 是全局单调递增的序列号（Lamport 时钟），
 * 同一 Key 的多条记录中 seq 最大者即最新版本。
 */
export interface Entry {
  readonly key: string;
  readonly seq: number;
  readonly value: Cell;
}
