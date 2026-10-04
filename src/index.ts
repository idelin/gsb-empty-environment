export { tokenize, KEYWORDS, type Token, type TokenKind } from './lexer.ts';
export { parse } from './parser.ts';
export {
  bindFile,
  bindProject,
  resolve,
  resolveModulePath,
  type BoundFile,
  type BoundProject,
  type Decl,
  type DeclKind,
  type Ref,
  type Scope,
} from './binder.ts';
export {
  planRename,
  applyPatch,
  applyEdits,
  verifyAfterEdits,
  hashProject,
  toProject,
  type ApplyResult,
  type Binding,
  type Conflict,
  type ConflictKind,
  type Edit,
  type Patch,
  type PlanResult,
} from './rename.ts';
