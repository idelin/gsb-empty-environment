import type {
  JsonPathErrorCode,
  JsonPathEvaluationErrorLike,
  JsonPathMode,
} from "./types.js";

export class JsonPathParseError extends SyntaxError implements JsonPathEvaluationErrorLike {
  readonly code = "PARSE_ERROR" as const;
  readonly expression: string;
  readonly offset: number;

  constructor(message: string, expression: string, offset: number) {
    super(`${message} at offset ${offset}`);
    this.name = "JsonPathParseError";
    this.expression = expression;
    this.offset = offset;
  }
}

export class JsonPathEvaluationError
  extends Error
  implements JsonPathEvaluationErrorLike
{
  readonly code: JsonPathErrorCode;
  readonly mode?: JsonPathMode;
  readonly offset?: number;
  readonly details?: Readonly<Record<string, unknown>>;

  constructor(
    code: JsonPathErrorCode,
    message: string,
    options: {
      mode?: JsonPathMode;
      offset?: number;
      cause?: unknown;
      details?: Readonly<Record<string, unknown>>;
    } = {},
  ) {
    super(message, options.cause === undefined ? undefined : { cause: options.cause });
    this.name = "JsonPathEvaluationError";
    this.code = code;
    this.mode = options.mode;
    this.offset = options.offset;
    this.details = options.details;
  }
}

export function isJsonPathError(error: unknown): error is Error & {
  code: JsonPathErrorCode;
} {
  return (
    error instanceof JsonPathEvaluationError ||
    error instanceof JsonPathParseError ||
    (error instanceof Error &&
      typeof (error as { code?: unknown }).code === "string" &&
      (error as unknown as { code: string }).code.startsWith("PARSE_") ===
        true)
  );
}
