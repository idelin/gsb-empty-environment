/**
 * Core DOM-like node model used across the parser, sanitizer, serializer and
 * idempotency checker. Every node carries an explicit namespace so the
 * "browser re-parse" namespace transitions can be audited deterministically.
 */

export const NS_HTML = 'http://www.w3.org/1999/xhtml';
export const NS_SVG = 'http://www.w3.org/2000/svg';
export const NS_MATHML = 'http://www.w3.org/1998/Math/MathML';

export type Namespace = typeof NS_HTML | typeof NS_SVG | typeof NS_MATHML;

export interface SourceLocation {
  /** Zero-based offset into the original input document. */
  offset: number;
  /** One-based line number. */
  line: number;
  /** One-based column number (UTF-16 code units). */
  column: number;
}

export interface Attribute {
  /** Local attribute name as adjusted for SVG/MathML per the HTML5 spec. */
  name: string;
  /** Raw attribute name exactly as tokenized (pre name-adjustment). */
  rawName: string;
  /** Value with character references already decoded by the tokenizer. */
  value: string;
  location: SourceLocation;
}

export interface NodeBase {
  location: SourceLocation;
}

export interface TextNode extends NodeBase {
  type: 'text';
  data: string;
}

export interface CommentNode extends NodeBase {
  type: 'comment';
  data: string;
}

export interface ElementNode extends NodeBase {
  type: 'element';
  tagName: string;
  namespace: Namespace;
  attributes: Attribute[];
  children: ChildNode[];
  /** Self-closing flag from the tokenizer (relevant for foreign content). */
  selfClosing: boolean;
}

export interface DocumentNode extends NodeBase {
  type: 'document';
  children: ChildNode[];
}

export type ChildNode = TextNode | CommentNode | ElementNode;
export type AnyNode = DocumentNode | ChildNode;

/** Severity of a sanitizer finding. */
export type ViolationKind =
  | 'banned-element'
  | 'banned-attribute'
  | 'banned-protocol'
  | 'dangerous-nesting'
  | 'dangerous-comment'
  | 'replacement-character'
  | 'idempotency-drift';

export interface Violation {
  kind: ViolationKind;
  message: string;
  location: SourceLocation;
  /** Human readable path, e.g. `html:math > mathml:mtext > html:table`. */
  path: string;
  /** The offending markup excerpt when available. */
  excerpt?: string;
}

export interface SanitizeResult {
  html: string;
  /** True when the sanitized output is stable across re-parse. */
  stable: boolean;
  violations: Violation[];
  /** Root of the sanitized in-memory tree. */
  tree: DocumentNode;
}

export interface SanitizeOptions {
  /** Reject inputs containing U+FFFD (default: true). */
  rejectReplacementChar?: boolean;
  /** Allow the `style` attribute with a conservative CSS guard. */
  allowStyleAttribute?: boolean;
  /** Keep well-formed comments instead of stripping every comment. */
  keepComments?: boolean;
  /** Extra URL schemes allowed in URI attributes. */
  allowedProtocols?: readonly string[];
}

/** Error thrown when input fails a hard security check. */
export class SanitizationError extends Error {
  readonly violations: Violation[];
  constructor(violations: Violation[]) {
    super(
      violations.length === 0
        ? 'Sanitization rejected'
        : `Sanitization rejected: ${violations[0].message} at line ${violations[0].location.line}:${violations[0].location.column}`,
    );
    this.name = 'SanitizationError';
    this.violations = violations;
  }
}
