/** 清单中单个分块的描述。 */
export interface ChunkSpec {
  index: number;
  size: number;
  sha256: string;
}

/** 清单中单个产物文件的描述。 */
export interface FileSpec {
  path: string;
  size: number;
  sha256: string;
  chunks: ChunkSpec[];
}

/** 远程执行器上报的产物清单。 */
export interface ArtifactManifest {
  taskId: string;
  inputDigest: string;
  toolVersion: string;
  attempt: number;
  files: FileSpec[];
}

/** 验收方当前期待的执行上下文（清单必须逐项匹配）。 */
export interface ExpectedContext {
  taskId: string;
  inputDigest: string;
  toolVersion: string;
  attempt: number;
}

/** 状态机状态。 */
export type AcceptanceState =
  | 'awaiting-manifest' // 等待清单
  | 'fetching'          // 清单已接受，等待/补取内容分块
  | 'verifying'         // 内容齐套，校验中
  | 'accepted'          // 已验收（终态）
  | 'failed';           // 校验失败，已清理（终态，需升级代次重新武装）

/** 分块引用，用于向执行器请求补取。 */
export interface ChunkRef {
  path: string;
  index: number;
}

export type RejectReason =
  | 'stale-attempt'      // 旧代次，不得覆盖已接受的新代次
  | 'context-mismatch'   // 输入摘要 / 工具版本 / 代次不匹配
  | 'duplicate-manifest' // 重复清单（幂等，不视为错误）
  | 'unexpected-state'   // 当前状态不允许该事件
  | 'unknown-chunk';     // 分块不属于已接受的清单

export interface Receipt {
  ok: boolean;
  reason?: RejectReason;
  state: AcceptanceState;
}
