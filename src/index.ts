export { tokenize, KEYWORDS, LexError, type Token, type TokenType } from './lexer.ts';
export { parse, ParseError } from './parser.ts';
export {
  resolveFile,
  type SymbolInfo,
  type SymbolKind,
  type Diagnostic,
  type DiagnosticCode,
  type FileAnalysis,
  type ImportSpecInfo,
} from './resolve.ts';
export { analyzeProject, computeVersion, resolveModulePath, type Project } from './project.ts';
export {
  preflightRename,
  applyRenamePatch,
  applyEdits,
  type RenameEdit,
  type RenamePatch,
  type Conflict,
  type ConflictReason,
  type PreflightResult,
  type ApplyResult,
} from './rename.ts';
