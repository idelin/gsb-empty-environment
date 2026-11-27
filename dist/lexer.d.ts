export type TokenType = "eof" | "dollar" | "at" | "dot" | "dotStar" | "recursive" | "lbracket" | "rbracket" | "lparen" | "rparen" | "comma" | "question" | "identifier" | "number" | "string" | "star" | "and" | "or" | "not" | "eq" | "ne" | "lt" | "le" | "gt" | "ge" | "plus" | "minus" | "slash" | "percent";
export interface Token {
    readonly type: TokenType;
    readonly value: string;
    readonly offset: number;
    readonly end: number;
}
export declare function tokenize(source: string): Token[];
//# sourceMappingURL=lexer.d.ts.map