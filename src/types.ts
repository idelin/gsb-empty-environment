/**
 * Public type surface for the pinned form-submission planner.
 *
 * The package is intentionally free of DOM lib dependencies: a TypeScript
 * host supplies plain snapshots captured from its document, and receives an
 * immutable, fully-qualified submit plan in return.
 */

export type HttpMethod = "GET" | "POST";

export type FormEncoding =
  | "application/x-www-form-urlencoded"
  | "multipart/form-data"
  | "text/plain";

/** A single form control entry. Binary values are modelled after {@link Blob}. */
export interface FormEntry {
  name: string;
  value: string | Blob;
  filename?: string;
}

/**
 * Submit-button attributes that may override the owning form. Every override
 * is resolved inside the same approval decision as the form-level attributes;
 * there is no submission path that bypasses {@link createSubmitPlan}.
 */
export interface ButtonOverride {
  name?: string;
  value?: string;
  formAction?: string;
  formMethod?: string;
  formEnctype?: string;
}

/** Immutable view of the document/form at the moment approval is requested. */
export interface FormSnapshot {
  /** Document generation token; bumps on every navigation. */
  documentGeneration: number;
  /** Document origin, e.g. https://app.example.test. */
  origin: string;
  /** Effective document base URL at capture time (after any <base> element). */
  baseUrl: string;
  /** Raw action attribute; relative values and the empty string are allowed. */
  action: string;
  /** Raw method attribute; defaults to GET when blank. */
  method: string;
  /** Raw enctype attribute; defaults to urlencoded when blank. */
  enctype: string;
  entries: FormEntry[];
}

/** Context handed to a host-supplied target permission policy. */
export interface PolicyContext {
  documentOrigin: string;
  targetOrigin: string;
  absoluteUrl: string;
  method: HttpMethod;
  enctype: FormEncoding;
  submitter: Readonly<ButtonOverride> | null;
}

export interface TargetPolicy {
  /** Called at approval time and again at submission time. */
  allow(context: PolicyContext): boolean;
}

export interface ApprovalOptions {
  /** Defaults to a same-origin policy. */
  policy?: TargetPolicy;
  /** Defaults to ["GET", "POST"]. */
  allowedMethods?: readonly string[];
  /** Defaults to the three HTML form encodings. */
  allowedEncTypes?: readonly string[];
}

/** The request target frozen at approval time. */
export interface PinnedTarget {
  absoluteUrl: string;
  origin: string;
  method: HttpMethod;
  enctype: FormEncoding;
}

/**
 * An approved submission. The object is deeply frozen: later mutations of the
 * live document (base href, form attributes, navigation) cannot influence it.
 */
export interface SubmitPlan {
  readonly id: string;
  readonly documentGeneration: number;
  readonly documentOrigin: string;
  readonly baseUrlAtApproval: string;
  readonly target: Readonly<PinnedTarget>;
  readonly submitter: Readonly<ButtonOverride> | null;
  readonly entries: readonly Readonly<FormEntry>[];
  readonly createdAt: number;
}

export interface PreparedRequest {
  url: string;
  method: HttpMethod;
  headers: Readonly<Record<string, string>>;
  body: Uint8Array | string | null;
}

/** Live document state consulted immediately before dispatch. */
export interface NavigationState {
  generation: number;
  origin: string;
  baseUrl: string;
}

/** Host-provided transport; it must send exactly the prepared request. */
export interface Transport {
  dispatch(request: PreparedRequest): Promise<TransportResult> | TransportResult;
}

export interface TransportResult {
  ok: boolean;
  status: number;
  /** Absolute URL the transport actually contacted. */
  url: string;
}

export type SubmitErrorCode =
  | "INVALID_SNAPSHOT"
  | "UNSAFE_BASE_URL"
  | "UNSAFE_ACTION_URL"
  | "METHOD_NOT_ALLOWED"
  | "ENCTYPE_NOT_ALLOWED"
  | "TARGET_DENIED"
  | "STALE_GENERATION"
  | "ORIGIN_MISMATCH"
  | "PERMISSION_REVOKED"
  | "TARGET_DRIFT";

export class SubmitError extends Error {
  readonly code: SubmitErrorCode;
  readonly details?: unknown;

  constructor(code: SubmitErrorCode, message: string, details?: unknown) {
    super(message);
    this.name = "SubmitError";
    this.code = code;
    this.details = details;
  }
}
