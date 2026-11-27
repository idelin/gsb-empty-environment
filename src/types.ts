/**
 * Public type definitions for the zero-dependency, raw-byte PDF structural
 * verification component.
 *
 * The component deliberately does NOT perform any cryptographic verification.
 * It validates the on-disk PDF structure (revision chain, /ByteRange coverage
 * and incremental-update integrity) so that a separate cryptographic layer can
 * safely trust the byte ranges that were actually signed.
 */

/** A parsed indirect-object reference (`<num> <gen> R`). */
export interface PdfRef {
  kind: 'ref';
  num: number;
  gen: number;
}

export interface PdfNull {
  kind: 'null';
}

export interface PdfBool {
  kind: 'bool';
  value: boolean;
}

export interface PdfNumber {
  kind: 'num';
  value: number;
}

/** A name object. The leading '/' is stripped and '#xx' escapes are decoded. */
export interface PdfName {
  kind: 'name';
  value: string;
}

/** A string object (literal or hex) stored as its raw decoded bytes. */
export interface PdfString {
  kind: 'string';
  value: Uint8Array;
}

export interface PdfArray {
  kind: 'array';
  items: PdfObject[];
}

export interface PdfDict {
  kind: 'dict';
  entries: Map<string, PdfObject>;
}

export interface PdfStream {
  kind: 'stream';
  dict: PdfDict;
  /** Undecoded, raw stream bytes exactly as stored on disk. */
  raw: Uint8Array;
}

export type PdfObject =
  | PdfRef
  | PdfNull
  | PdfBool
  | PdfNumber
  | PdfName
  | PdfString
  | PdfArray
  | PdfDict
  | PdfStream;

/** A single cross-reference entry as recorded by one revision. */
export interface XrefEntry {
  /** 0 = free, 1 = in-use (uncompressed), 2 = compressed (object stream). */
  type: 0 | 1 | 2;
  /** Byte offset for type 1; containing object-stream number for type 2. */
  offset: number;
  gen: number;
  /** Index inside the object stream (type 2 only). */
  streamIndex?: number;
}

/** One parsed PDF revision (one xref table / xref stream + trailer). */
export interface PdfRevision {
  /** 0 = oldest revision, increasing towards the latest revision. */
  index: number;
  /** Byte offset at which this revision's xref table/stream starts. */
  xrefOffset: number;
  /** Byte offset immediately after this revision's `%%EOF` line. */
  revisionEnd: number;
  /** Object number -> cross-reference entry. */
  entries: Map<number, XrefEntry>;
  trailer: PdfDict;
  /** Offset declared by the trailer's /Prev pointer, or null for the oldest. */
  prevOffset: number | null;
}

/** Structural information about a located signature field dictionary. */
export interface SignatureInfo {
  objectNumber: number;
  fieldName: string | null;
  /** `/Sig` approval signature or `/DocTimeStamp` document timestamp. */
  type: 'signature' | 'doc-timestamp';
  filter: string | null;
  subFilter: string | null;
  byteRange: number[];
  contentsHex: string;
  /** Signed byte spans as [startOffset, endOffsetExclusive) pairs. */
  signedDataRanges: Array<[number, number]>;
  /** Revision whose snapshot this signature covers. */
  signingRevisionIndex: number;
  /** True only when every structural /ByteRange rule is satisfied. */
  byteRangeValid: boolean;
  /**
   * Cryptographic PKCS#7 validation is intentionally out of scope; callers
   * must verify `signedDataRanges` + `contentsHex` with their own crypto layer.
   */
  cryptographic: 'not-verified';
}

export interface RevisionInfo {
  index: number;
  xrefOffset: number;
  revisionEnd: number;
  inUseObjectCount: number;
  prevOffset: number | null;
}

export type FindingSeverity = 'error' | 'warning';

export interface AuditFinding {
  code: string;
  severity: FindingSeverity;
  message: string;
  objectNumber?: number;
  revisionIndex?: number;
}

export interface PdfVerificationReport {
  valid: boolean;
  headerValid: boolean;
  fileSize: number;
  revisionCount: number;
  revisions: RevisionInfo[];
  signatures: SignatureInfo[];
  findings: AuditFinding[];
  signedObjectCount: number | null;
}
