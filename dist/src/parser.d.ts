import type { Expr } from "./ast.js";
import type { Type } from "./types.js";
export declare class ParseError extends Error {
    readonly position: number;
    constructor(message: string, position: number);
}
export type TokenKind = "ident" | "typevar" | "number" | "string" | "punct" | "eof";
export interface Token {
    readonly kind: TokenKind;
    readonly value: string;
    readonly position: number;
}
export declare function tokenize(source: string): Token[];
export declare function parse(source: string): Expr;
export declare const parseExpression: typeof parse;
export declare const parseExpr: typeof parse;
export declare function parseType(source: string): Type;
export declare const parseTypeText: typeof parseType;
