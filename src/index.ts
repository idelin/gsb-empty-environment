export { TextDocument, type Position } from './document.ts';
export {
  IncrementalLexer,
  lexAll,
  lexToken,
  LS_BLOCK_COMMENT,
  LS_CODE,
  MATCH_CLOSE,
  MATCH_OPEN,
  OPENERS,
  CLOSERS,
  type LexState,
  type Token,
  type TokenType,
} from './lexer.ts';
export {
  analyzeBrackets,
  type BracketAnalysis,
  type BracketPair,
  type Diagnostic,
  type DiagnosticCode,
} from './brackets.ts';
export { LanguageService } from './service.ts';
export {
  clampToSurrogateBoundary,
  isHighSurrogate,
  isLowSurrogate,
  splitsSurrogatePair,
} from './textutil.ts';
