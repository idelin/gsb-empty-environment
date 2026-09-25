export type ErrorSource =
  | "template"
  | "params"
  | "constraints"
  | "identity"
  | "cursor";

/** Error that always carries a JSON path and the input section it came from. */
export class ExpandError extends Error {
  readonly source: ErrorSource;
  readonly path: string;

  constructor(source: ErrorSource, path: string, message: string) {
    super(`${message} [source=${source} path=${path}]`);
    this.name = "ExpandError";
    this.source = source;
    this.path = path;
  }
}
