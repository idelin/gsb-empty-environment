import type { SCHEMA } from "./core.ts";

export type ContextId = string;

export const Contexts = {
  HtmlText: "text/html#text",
  HtmlDoubleAttribute: "text/html#double-quoted-attribute-value",
  HtmlSingleAttribute: "text/html#single-quoted-attribute-value",
  HtmlUnquotedAttribute: "text/html#unquoted-attribute-value",
  JsDoubleString: "application/javascript#double-quoted-string",
  UrlQueryComponent: "text/uri#query-component",
  UrlPathComponent: "text/uri#path-component",
  Raw: "application/x-raw",
} as const;

export interface TemplateNodeBase {
  id: string;
}

export interface LiteralNode extends TemplateNodeBase {
  kind: "literal";
  value: string;
  contextAfter?: ContextId;
}

export interface FieldNode extends TemplateNodeBase {
  kind: "field";
  bindingId: string;
  sourceLabel: string;
  encoderId: string;
  stateId?: string;
  stateAfterId?: string;
  contextId: ContextId;
  authorizedContextIds: ContextId[];
  contextAfter?: ContextId;
}

export interface CallNode extends TemplateNodeBase {
  kind: "call";
  targetFragmentId: string;
  returnPointId: string;
  contextAfter?: ContextId;
  conditionBindingId?: string;
  truthyValues?: string[];
}

export interface ReturnNode extends TemplateNodeBase {
  kind: "return";
}

export interface BoundaryNode extends TemplateNodeBase {
  kind: "boundary";
  boundaryId: string;
  alignmentKey: string;
  contextAfter?: ContextId;
}

export type TemplateNode =
  | LiteralNode
  | FieldNode
  | CallNode
  | ReturnNode
  | BoundaryNode;

export interface TemplateFragment {
  id: string;
  nodes: TemplateNode[];
}

export interface TemplateDocument {
  schema: typeof SCHEMA.template;
  id: string;
  version: string;
  entryFragmentId: string;
  fragments: Record<string, TemplateFragment>;
}

export interface EncoderStateDef {
  contextId: ContextId;
  escape?: Record<string, string>;
  next?: Record<string, string>;
  comment?: string;
}

export interface EncoderTable {
  schema: typeof SCHEMA.encoderTable;
  id: string;
  version: string;
  initialStateId: string;
  states: Record<string, EncoderStateDef>;
}

export type EncoderSet = Record<string, EncoderTable>;

export interface ReceiptSegment {
  ordinal: number;
  start: number;
  end: number;
  fragmentId: string;
  nodeId: string;
  kind: "literal" | "field";
  bindingId?: string;
  sourceLabel?: string;
  contextId: ContextId;
  encoderId?: string;
  encoderStateBefore?: string;
  encoderStateAfter?: string;
  emittedSha256: string;
}

export interface PrefixReceipt {
  schema: typeof SCHEMA.receipt;
  receiptId: string;
  templateId: string;
  templateVersion: string;
  oldTemplateHash: string;
  boundaryId: string;
  alignmentKey: string;
  byteLength: number;
  prefixSha256: string;
  segments: ReceiptSegment[];
  finalContextId: ContextId;
  finalEncoderRef?: EncoderStateRef;
  openCallSiteIds: string[];
  stackSummarySha256?: string;
  issuedAt?: string;
  nonce?: string;
}

export interface AuthenticatedReceipt extends PrefixReceipt {
  auth: {
    alg: "HS256-SHA256/v1";
    keyId: string;
    mac: string;
  };
}

export interface ReceiptAuthenticator {
  readonly keyId: string;
  readonly alg: string;
  authenticate(receipt: PrefixReceipt): AuthenticatedReceipt;
  verify(receipt: AuthenticatedReceipt): boolean;
}

export interface EncoderStateRef {
  encoderId: string;
  stateId: string;
}

export interface FrameObligations {
  contextId: ContextId;
  encoderRef?: EncoderStateRef;
  activeBindingIds: string[];
  requiredCloseTokens: string[];
}

export interface StackFrameSummary extends FrameObligations {
  id: string;
  fragmentId: string;
  resumeNodeId: string;
  callSiteNodeId?: string;
  returnPointNodeId?: string;
}

export interface StackEdge {
  callerFrameId: string;
  callSiteNodeId: string;
  calleeFrameId: string;
}

export interface StackSummary {
  schema: typeof SCHEMA.stackSummary;
  summaryId: string;
  oldTemplateHash: string;
  boundaryId: string;
  alignmentKey: string;
  rootFrameId: string;
  rootFrameIds?: string[];
  possibleTopFrameIds: string[];
  frames: Record<string, StackFrameSummary>;
  edges: StackEdge[];
  complete: boolean;
  incompletenessReasons: string[];
}

export interface EncoderStateBinding {
  oldEncoderId: string;
  oldStateId: string;
  newEncoderId: string;
  newStateId: string;
  mergeClass?: string;
}

export interface FrameContinuation {
  newFrameId?: string;
  newFragmentId: string;
  newResumeNodeId: string;
  contextId: ContextId;
  encoderRef?: EncoderStateRef;
  obligationOverrides?: Partial<FrameObligations>;
  mergeGroupId?: string;
}

export interface ReturnPointMergeClaim {
  mergeGroupId: string;
  oldReturnPointNodeIds: string[];
  newReturnPointNodeId: string;
}

export interface MigrationCertificate {
  schema: typeof SCHEMA.certificate;
  certificateId: string;
  boundaryId: string;
  alignmentKey: string;
  inputHashes: {
    oldTemplate: string;
    newTemplate: string;
    oldEncoders: string;
    newEncoders: string;
    stackSummary: string;
    receipt: string;
  };
  continuations: Record<string, FrameContinuation>;
  encoderBindings: EncoderStateBinding[];
  returnPointMerges: ReturnPointMergeClaim[];
  universalProof: {
    summaryKind: "regular-stack-graph/v1";
    relevantFrameIds: string[];
    cycleInvariantFrameIds: string[];
    receiptCompatibility: "bound-hash-and-context/v1" | "context-only/v1";
  };
  issuedAt?: string;
  issuerKeyId?: string;
  certificateMac?: string;
}

export interface HostDataEnvelope {
  value: string;
  sourceLabel: string;
}

export type HostData = Record<string, HostDataEnvelope>;
