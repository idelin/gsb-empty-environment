export class JsonPathParseError extends SyntaxError {
    code = "PARSE_ERROR";
    expression;
    offset;
    constructor(message, expression, offset) {
        super(`${message} at offset ${offset}`);
        this.name = "JsonPathParseError";
        this.expression = expression;
        this.offset = offset;
    }
}
export class JsonPathEvaluationError extends Error {
    code;
    mode;
    offset;
    details;
    constructor(code, message, options = {}) {
        super(message, options.cause === undefined ? undefined : { cause: options.cause });
        this.name = "JsonPathEvaluationError";
        this.code = code;
        this.mode = options.mode;
        this.offset = options.offset;
        this.details = options.details;
    }
}
export function isJsonPathError(error) {
    return (error instanceof JsonPathEvaluationError ||
        error instanceof JsonPathParseError ||
        (error instanceof Error &&
            typeof error.code === "string" &&
            error.code.startsWith("PARSE_") ===
                true));
}
//# sourceMappingURL=errors.js.map