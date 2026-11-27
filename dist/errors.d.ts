import type { JsonPathErrorCode, JsonPathEvaluationErrorLike, JsonPathMode } from "./types.js";
export declare class JsonPathParseError extends SyntaxError implements JsonPathEvaluationErrorLike {
    readonly code: "PARSE_ERROR";
    readonly expression: string;
    readonly offset: number;
    constructor(message: string, expression: string, offset: number);
}
export declare class JsonPathEvaluationError extends Error implements JsonPathEvaluationErrorLike {
    readonly code: JsonPathErrorCode;
    readonly mode?: JsonPathMode;
    readonly offset?: number;
    readonly details?: Readonly<Record<string, unknown>>;
    constructor(code: JsonPathErrorCode, message: string, options?: {
        mode?: JsonPathMode;
        offset?: number;
        cause?: unknown;
        details?: Readonly<Record<string, unknown>>;
    });
}
export declare function isJsonPathError(error: unknown): error is Error & {
    code: JsonPathErrorCode;
};
//# sourceMappingURL=errors.d.ts.map