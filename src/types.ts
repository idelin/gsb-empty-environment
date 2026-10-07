// 共享类型定义。本文件只包含类型，构建时会被整体擦除。

export type Assoc = 'start' | 'end';

// 一个在旧文本上确定的匹配（冻结后不再变化）。
export interface MatchRecord {
  start: number;
  end: number;
  text: string;
  groups: Array<string | undefined>;
  namedGroups: Record<string, string | undefined> | undefined;
}

// 单个可逆编辑：旧区间 [oldStart, oldEnd) 被替换为新区间 [newStart, newEnd)。
export interface Edit {
  oldStart: number;
  oldEnd: number;
  newStart: number;
  newEnd: number;
  oldText: string;
  newText: string;
}

export interface ReplaceResult {
  oldText: string;
  newText: string;
  matches: MatchRecord[];
  edits: Edit[];
  // 旧偏移 -> 新偏移。替换区域内部的位置按 assoc 决定落在替换文本起点还是终点；
  // 零宽编辑所在边界同理（start=插入点之前，end=插入点之后）。
  mapPosition: (pos: number, assoc?: Assoc) => number;
  mapRange: (start: number, end: number) => [number, number];
}

export type ReplacementFn = (matchText: string, groups: Array<string | undefined>, offset: number, text: string) => string;

export type Replacement = string | ReplacementFn;

export interface DocSnapshot {
  text: string;
  version: number;
}

export interface ReplaceOp {
  docId: string;
  expectedVersion: number;
  pattern: RegExp | string;
  replacement: Replacement;
}

export interface VersionConflict {
  docId: string;
  expectedVersion: number;
  actualVersion: number;
}

export interface AppliedDocEdit {
  docId: string;
  oldVersion: number;
  newVersion: number;
  oldText: string;
  newText: string;
  edits: Edit[];
  result: ReplaceResult;
}

export interface TxnSuccess {
  ok: true;
  applied: AppliedDocEdit[];
  undo: () => void;
}

export interface TxnFailure {
  ok: false;
  reason: string;
  conflicts: VersionConflict[];
}

export type TxnResult = TxnSuccess | TxnFailure;
