export type {
  AffectedChangeKind,
  AffectedReference,
  Definition,
  Diagnostic,
  DiagnosticCode,
  Heading,
  ParseResult,
  Position,
  Range,
  Reference,
  TextEdit,
  UpdateResult,
} from './types.ts';
export { normalizeName, parseDocument } from './parser.ts';
export { Document } from './document.ts';
