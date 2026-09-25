import { formatPath, type JsonValue, type PathSeg } from "./json.ts";

export type ErrorCode =
  | "TYPE_CONFLICT"
  | "MISSING_ID"
  | "REF_NOT_FOUND"
  | "REF_CYCLE"
  | "INVALID_LAYER"
  | "INVALID_OP"
  | "VALIDATION";

/** Every error carries a structured path (array of segments), never a pre-formatted string. */
export class ConfigError extends Error {
  readonly code: ErrorCode;
  readonly path: PathSeg[];
  readonly layer?: string;
  readonly details?: JsonValue;

  constructor(code: ErrorCode, message: string, opts: { path?: PathSeg[]; layer?: string; details?: JsonValue } = {}) {
    super(`${message} (at ${formatPath(opts.path ?? [])}${opts.layer ? `, layer "${opts.layer}"` : ""})`);
    this.name = "ConfigError";
    this.code = code;
    this.path = [...(opts.path ?? [])];
    this.layer = opts.layer;
    this.details = opts.details;
  }
}

export class RefCycleError extends ConfigError {
  /** The cycle as a chain of field paths, first == last. */
  readonly cycle: PathSeg[][];
  constructor(cycle: PathSeg[][]) {
    super("REF_CYCLE", `reference cycle of length ${cycle.length - 1}`, {
      path: cycle[0] ?? [],
      details: { cycle: cycle.map((p) => formatPath(p)) },
    });
    this.cycle = cycle.map((p) => [...p]);
  }
}
