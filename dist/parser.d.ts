import type { JsonPath, ParseOptions } from "./types.js";
export declare class JsonPathParser {
    private readonly tokens;
    private readonly source;
    private cursor;
    constructor(source: string);
    parse(options?: ParseOptions): JsonPath;
    private parsePathExpression;
    private parsePathSteps;
    private parseDotStep;
    private parseMethod;
    private parseLiteralArgument;
    private parseArrayStep;
    private toIndexExpression;
    private parseExpression;
    private parseUnary;
    private parsePrimary;
    private peek;
    private advance;
    private expect;
    private error;
}
export declare function parseJsonPath(source: string, options?: ParseOptions): JsonPath;
//# sourceMappingURL=parser.d.ts.map