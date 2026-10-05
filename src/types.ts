export interface Position {
  line: number;
  column: number;
}

export interface SourceRange {
  file: string;
  start: Position;
  end: Position;
}

/**
 * 旧版本中的语句覆盖记录。
 * 语句的身份由 id 与内容指纹共同构成，与所在行号无关。
 */
export interface OldStatement {
  id: string;
  range: SourceRange;
  hits: number;
  /** 归一化源码内容的指纹，见 fingerprint.ts */
  fingerprint: string;
}

/** 新版本中的语句（尚无覆盖数据）。 */
export interface NewStatement {
  id: string;
  range: SourceRange;
  fingerprint: string;
}

export interface OldCoverageFile {
  version: 1;
  statements: OldStatement[];
}

export interface NewStatementsFile {
  version: 1;
  statements: NewStatement[];
}
