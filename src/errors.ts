export type JsonNormalizationErrorCode =
  | "EMPTY_INPUT"
  | "BODY_TOO_LARGE"
  | "INVALID_UTF8"
  | "INVALID_CHARACTER"
  | "INVALID_ESCAPE"
  | "INVALID_SURROGATE"
  | "INVALID_NUMBER"
  | "UNEXPECTED_EOF"
  | "UNEXPECTED_TOKEN"
  | "EXPECTED_KEY"
  | "EXPECTED_COLON"
  | "EXPECTED_COMMA_OR_END"
  | "TRAILING_COMMA"
  | "TRAILING_DATA"
  | "DEPTH_EXCEEDED";

export interface JsonNormalizationErrorOptions {
  offset?: number;
  path?: string;
  cause?: unknown;
}

/**
 * Error raised by the strict JSON reader. Offsets are UTF-8 byte offsets into
 * the original request body, not JavaScript string indexes.
 */
export class JsonNormalizationError extends Error {
  readonly code: JsonNormalizationErrorCode;
  readonly offset?: number;
  readonly path?: string;
  readonly cause?: unknown;

  constructor(
    code: JsonNormalizationErrorCode,
    message: string,
    options: JsonNormalizationErrorOptions = {},
  ) {
    super(message);
    this.name = "JsonNormalizationError";
    this.code = code;
    this.offset = options.offset;
    this.path = options.path;
    if ("cause" in options) {
      this.cause = options.cause;
    }
  }

  toJSON(): {
    name: string;
    code: JsonNormalizationErrorCode;
    message: string;
    offset?: number;
    path?: string;
  } {
    return {
      name: this.name,
      code: this.code,
      message: this.message,
      ...(this.offset === undefined ? {} : { offset: this.offset }),
      ...(this.path === undefined ? {} : { path: this.path }),
    };
  }
}
