export type LinkErrorCode =
  | "DUPLICATE_MODULE"
  | "MODULE_NOT_FOUND"
  | "VERSION_MISMATCH"
  | "MISSING_EXPORT"
  | "SIGNATURE_MISMATCH"
  | "EVAL_CYCLE"
  | "INIT_FAILURE";

export class LinkError extends Error {
  readonly code: LinkErrorCode;
  /** 最短错误来源链，如 ["a@1.0.0", "b@2.0.0", "a@1.0.0"]。 */
  readonly chain: string[];
  /** 出错导入语句位置（如有）。 */
  readonly loc?: string;

  constructor(code: LinkErrorCode, message: string, chain: string[], loc?: string) {
    super(message);
    this.name = "LinkError";
    this.code = code;
    this.chain = chain;
    this.loc = loc;
  }
}

/** 批量链接失败：携带全部错误；抛出时注册表不发生任何换版。 */
export class LinkFailure extends Error {
  readonly errors: LinkError[];

  constructor(errors: LinkError[]) {
    super(errors.map((e) => `[${e.code}] ${e.message}`).join("\n"));
    this.name = "LinkFailure";
    this.errors = errors;
  }
}
