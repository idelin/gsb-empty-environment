export type Key = string;

export type OpKind = 'put' | 'del';

/** 甲上被接受的一次写（进入 oplog 的变更） */
export interface Op {
  seq: number;
  key: Key;
  kind: OpKind;
  value: string | null; // put: 写入的值; del: null
}

/** 乙侧存储条目；value 为 null 表示墓碑（已删除，不可见但保留版本） */
export interface Entry {
  seq: number;
  value: string | null;
}

export type WriteResult =
  | { ok: true; op: Op }
  | { ok: false; reason: 'fenced' };
