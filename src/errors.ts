import { formatPath, type Path } from "./json.ts";

/** 单条记录在某个变换步骤上的失败，定位到记录内的具体 JSON 路径。 */
export class TransformError extends Error {
  readonly recordId: string;
  readonly step: string;
  readonly path: Path;

  constructor(recordId: string, step: string, path: Path, message: string, options?: { cause?: unknown }) {
    super(`record=${recordId} step=${step} path=${formatPath(path)}: ${message}`, options);
    this.name = "TransformError";
    this.recordId = recordId;
    this.step = step;
    this.path = [...path];
  }

  toJSON(): SerializedError {
    return {
      recordId: this.recordId,
      step: this.step,
      path: [...this.path],
      message: this.message,
    };
  }

  static fromJSON(raw: SerializedError): TransformError {
    return new TransformError(raw.recordId, raw.step, raw.path, raw.message);
  }
}

export interface SerializedError {
  recordId: string;
  step: string;
  path: (string | number)[];
  message: string;
}

/** 原子模式下整批失败：不发布任何结果，cause 为首个 TransformError。 */
export class BatchError extends Error {
  readonly recordId: string;
  readonly step: string;

  constructor(cause: TransformError) {
    super(`batch aborted at record=${cause.recordId} step=${cause.step}: ${cause.message}`, { cause });
    this.name = "BatchError";
    this.recordId = cause.recordId;
    this.step = cause.step;
  }
}
