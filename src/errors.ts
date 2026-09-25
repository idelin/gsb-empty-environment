/** 所有错误均携带定位到具体路径的 path（JSON 路径风格，如 $.params.axes.color）。 */
export class ExpandError extends Error {
  readonly path: string;

  constructor(path: string, message: string) {
    super(`${path}: ${message}`);
    this.name = "ExpandError";
    this.path = path;
  }
}
