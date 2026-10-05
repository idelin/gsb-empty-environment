export { pruneSource, type PruneOptions, type PruneResult } from './prune.ts';
export { LineMapping, type Position, type LineRange } from './mapping.ts';
export { scanLines, type Directive, type ScannedLine, type LexState } from './lexer.ts';
export { parseExpression, evaluate, type Expr, type VarTable, type VarValue } from './expr.ts';
export { PruneError, ExpressionError, UnknownVariableError, DirectiveError } from './errors.ts';
