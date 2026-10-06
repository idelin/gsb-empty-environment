export { parse, ParseError } from "./parser.ts";
export { lex, LexError, KEYWORDS } from "./lexer.ts";
export { bind, resolveName } from "./binder.ts";
export type { Binding, BindResult, Scope } from "./binder.ts";
export { rename } from "./rename.ts";
export type { RenameResult, TextEdit } from "./rename.ts";
export { computePaths, verifyIsomorphism } from "./verify.ts";
export * from "./ast.ts";
