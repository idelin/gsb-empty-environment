import { randomUUID } from "node:crypto";
import { SCHEMA } from "./core.ts";
import { prefixSha256 } from "./receipts.ts";
import { sourceLabelsCompatible } from "./templates.ts";
import type { MigrationVerificationResult } from "./certificate.ts";
import type {
  ContextId,
  EncoderSet,
  EncoderStateRef,
  HostDataEnvelope,
  PrefixReceipt,
  ReceiptSegment,
  StackSummary,
  TemplateDocument,
} from "./types.ts";
import { encodeValue } from "./encoders.ts";

export interface LiveFrame {
  instanceId: string;
  kindId: string;
  fragmentId: string;
  pc: number;
  resumeNodeId: string;
  contextId: ContextId;
  encoderRef?: EncoderStateRef;
  activeBindingIds: string[];
  requiredCloseTokens: string[];
  callSiteNodeId?: string;
  returnPointNodeId?: string;
}

export interface HostEvent {
  sequence: number;
  type: "literal" | "field" | "call" | "return" | "boundary" | "migrate" | "complete";
  at?: string;
  detail?: Record<string, unknown>;
}

export interface RenderSession {
  sessionId: string;
  templateHash?: string;
  templateId: string;
  templateVersion: string;
  boundaryId?: string;
  alignmentKey?: string;
  output: string;
  byteLength: number;
  segments: ReceiptSegment[];
  events: HostEvent[];
  stack: LiveFrame[];
  status: "running" | "boundary" | "complete" | "failed" | "quarantined";
  steps: number;
}

export type HostDataProvider =
  | Record<string, HostDataEnvelope>
  | ((request: DataRequest) => HostDataEnvelope | undefined);

export interface DataRequest {
  bindingId: string;
  nodeId: string;
  fragmentId: string;
  depth: number;
  frameKindId: string;
}

export interface RenderArgs {
  template: TemplateDocument;
  encoders: EncoderSet;
  data: HostDataProvider;
  entryFragmentId?: string;
  initialContextId?: ContextId;
  requiredCloseTokens?: string[];
  maxSteps?: number;
  sessionId?: string;
}

export interface RunToBoundaryArgs extends RenderArgs {
  boundaryId?: string;
}

export function createSession(args: RenderArgs): RenderSession {
  const entryFragmentId = args.entryFragmentId ?? args.template.entryFragmentId;
  const firstNode = args.template.fragments[entryFragmentId]?.nodes[0];
  const rootResumeNodeId = firstNode?.id ?? "__entry__";
  const rootKindId = `${entryFragmentId}:${rootResumeNodeId}`;
  return {
    sessionId: args.sessionId ?? randomUUID(),
    templateId: args.template.id,
    templateVersion: args.template.version,
    output: "",
    byteLength: 0,
    segments: [],
    events: [],
    stack: [
      {
        instanceId: "frame-root",
        kindId: rootKindId,
        fragmentId: entryFragmentId,
        pc: 0,
        resumeNodeId: rootResumeNodeId,
        contextId: args.initialContextId ?? "text/html#text",
        activeBindingIds: [],
        requiredCloseTokens: args.requiredCloseTokens ?? [],
      },
    ],
    status: "running",
    steps: 0,
  };
}

export function runToBoundary(args: RunToBoundaryArgs): RenderSession {
  const session = createSession(args);
  stepUntil({
    session,
    template: args.template,
    encoders: args.encoders,
    data: args.data,
    stopBoundaryId: args.boundaryId,
    stopAtFirstBoundary: args.boundaryId === undefined,
    maxSteps: args.maxSteps ?? 10_000,
  });
  return session;
}

export function renderToCompletion(args: RenderArgs): RenderSession {
  const session = createSession(args);
  stepUntil({
    session,
    template: args.template,
    encoders: args.encoders,
    data: args.data,
    stopAtFirstBoundary: false,
    maxSteps: args.maxSteps ?? 100_000,
  });
  return session;
}

interface StepArgs {
  session: RenderSession;
  template: TemplateDocument;
  encoders: EncoderSet;
  data: HostDataProvider;
  stopBoundaryId?: string;
  stopAtFirstBoundary: boolean;
  maxSteps: number;
}

function stepUntil(args: StepArgs): void {
  const { session, template } = args;

  while (session.stack.length > 0 && session.steps < args.maxSteps) {
    session.steps += 1;
    const frame = session.stack[session.stack.length - 1];
    const fragment = template.fragments[frame.fragmentId];
    if (!fragment) {
      session.status = "failed";
      throw new Error(`Missing fragment ${frame.fragmentId}`);
    }
    if (frame.pc >= fragment.nodes.length) {
      popFrame(session);
      continue;
    }

    const node = fragment.nodes[frame.pc];
    frame.resumeNodeId = node.id;
    frame.kindId = `${frame.fragmentId}:${node.id}`;

    if (node.kind === "return") {
      appendEvent(session, "return", node.id, { fragmentId: frame.fragmentId });
      popFrame(session);
      continue;
    }

    if (node.kind === "boundary") {
      appendEvent(session, "boundary", node.id, {
        boundaryId: node.boundaryId,
        alignmentKey: node.alignmentKey,
      });
      session.boundaryId = node.boundaryId;
      session.alignmentKey = node.alignmentKey;
      if (args.stopAtFirstBoundary || node.boundaryId === args.stopBoundaryId) {
        session.status = "boundary";
        return;
      }
      frame.contextId = node.contextAfter ?? frame.contextId;
      frame.pc += 1;
      continue;
    }

    if (node.kind === "literal") {
      appendOutput(session, template, frame.fragmentId, node.id, "literal", node.value, {
        contextId: node.contextAfter ?? frame.contextId,
      });
      appendEvent(session, "literal", node.id, { bytes: Buffer.byteLength(node.value) });
      if (node.contextAfter && node.contextAfter !== frame.contextId) {
        frame.encoderRef = undefined;
      }
      frame.contextId = node.contextAfter ?? frame.contextId;
      frame.pc += 1;
      continue;
    }

    if (node.kind === "field") {
      const envelope = resolveData(args.data, node.bindingId, node.id, frame, session.stack.length - 1);
      if (!envelope) {
        session.status = "failed";
        throw new Error(`Missing data for binding ${node.bindingId}`);
      }
      if (!sourceLabelsCompatible(node.sourceLabel, envelope.sourceLabel)) {
        session.status = "failed";
        throw new Error(
          `Source label violation for ${node.bindingId}: template requires ${node.sourceLabel}, got ${envelope.sourceLabel}`,
        );
      }
      if (!node.authorizedContextIds.includes(frame.contextId)) {
        session.status = "failed";
        throw new Error(`Field ${node.bindingId} is not authorized in context ${frame.contextId}`);
      }

      const table = args.encoders[node.encoderId];
      if (!table) throw new Error(`Missing encoder ${node.encoderId}`);
      const stateBefore = node.stateId ?? table.initialStateId;
      const encoded = encodeValue(table, stateBefore, envelope.value);
      appendOutput(session, template, frame.fragmentId, node.id, "field", encoded.output, {
        contextId: node.contextId,
        bindingId: node.bindingId,
        sourceLabel: envelope.sourceLabel,
        encoderId: node.encoderId,
        encoderStateBefore: stateBefore,
        encoderStateAfter: encoded.stateIdAfter,
      });
      appendEvent(session, "field", node.id, {
        bindingId: node.bindingId,
        sourceLabel: envelope.sourceLabel,
        encoded: encoded.output,
      });
      frame.encoderRef = { encoderId: node.encoderId, stateId: node.stateAfterId ?? encoded.stateIdAfter };
      frame.contextId = node.contextAfter ?? frame.contextId;
      frame.pc += 1;
      continue;
    }

    if (node.kind === "call") {
      if (node.conditionBindingId) {
        const condition = resolveData(
          args.data,
          node.conditionBindingId,
          node.id,
          frame,
          session.stack.length - 1,
        );
        const truthy = new Set(node.truthyValues ?? ["1", "true", "yes", "y"]);
        if (!condition || !truthy.has(condition.value)) {
          frame.pc += 1;
          continue;
        }
      }

      const returnIndex = template.fragments[frame.fragmentId].nodes.findIndex(
        (candidate) => candidate.id === node.returnPointId,
      );
      if (returnIndex < 0) throw new Error(`Missing return point ${node.returnPointId}`);
      frame.pc = returnIndex;
      frame.resumeNodeId = node.returnPointId;
      frame.returnPointNodeId = node.returnPointId;
      frame.callSiteNodeId = node.id;
      frame.kindId = `${frame.fragmentId}:${node.returnPointId}`;

      const target = template.fragments[node.targetFragmentId];
      if (!target) throw new Error(`Missing call target ${node.targetFragmentId}`);
      const childFirst = target.nodes[0];
      const childKindId = childFirst ? `${node.targetFragmentId}:${childFirst.id}` : node.targetFragmentId;
      session.stack.push({
        instanceId: randomUUID(),
        kindId: childKindId,
        fragmentId: node.targetFragmentId,
        pc: 0,
        resumeNodeId: childFirst?.id ?? "__return__",
        contextId: node.contextAfter ?? frame.contextId,
        encoderRef: frame.encoderRef,
        activeBindingIds: [],
        requiredCloseTokens: [],
      });
      appendEvent(session, "call", node.id, {
        target: node.targetFragmentId,
        depth: session.stack.length,
        returnPointId: node.returnPointId,
      });
      continue;
    }
  }

  if (session.stack.length === 0) {
    session.status = "complete";
    appendEvent(session, "complete");
  } else if (session.steps >= args.maxSteps) {
    session.status = "failed";
    throw new Error("Host rendering exceeded maxSteps");
  }
}

function popFrame(session: RenderSession): void {
  const popped = session.stack.pop();
  if (popped) {
    appendEvent(session, "return", popped.resumeNodeId, { kindId: popped.kindId });
  }
}

function resolveData(
  provider: HostDataProvider,
  bindingId: string,
  nodeId: string,
  frame: LiveFrame,
  depth: number,
): HostDataEnvelope | undefined {
  if (typeof provider === "function") {
    return provider({
      bindingId,
      nodeId,
      fragmentId: frame.fragmentId,
      depth,
      frameKindId: frame.kindId,
    });
  }
  return provider[bindingId];
}

interface OutputMetadata {
  contextId: ContextId;
  bindingId?: string;
  sourceLabel?: string;
  encoderId?: string;
  encoderStateBefore?: string;
  encoderStateAfter?: string;
}

function appendOutput(
  session: RenderSession,
  template: TemplateDocument,
  fragmentId: string,
  nodeId: string,
  kind: "literal" | "field",
  text: string,
  metadata: OutputMetadata,
): void {
  const start = session.byteLength;
  session.output += text;
  session.byteLength = Buffer.byteLength(session.output, "utf8");
  const end = session.byteLength;
  const segment: ReceiptSegment = {
    ordinal: session.segments.length,
    start,
    end,
    fragmentId,
    nodeId,
    kind,
    bindingId: metadata.bindingId,
    sourceLabel: metadata.sourceLabel ?? "source:template-literal/trusted",
    contextId: metadata.contextId,
    encoderId: metadata.encoderId,
    encoderStateBefore: metadata.encoderStateBefore,
    encoderStateAfter: metadata.encoderStateAfter,
    emittedSha256: prefixSha256(text),
  };
  session.segments.push(segment);
  if (!session.templateHash) session.templateId = template.id;
}

function appendEvent(
  session: RenderSession,
  type: HostEvent["type"],
  at?: string,
  detail?: Record<string, unknown>,
): void {
  session.events.push({ sequence: session.events.length, type, at, detail });
}

export interface ReceiptDraftFromSessionArgs {
  receiptId: string;
  oldTemplateHash: string;
  stackSummaryHash?: string;
  issuedAt?: string;
  nonce?: string;
}

export function receiptDraftFromSession(
  session: RenderSession,
  args: ReceiptDraftFromSessionArgs,
): PrefixReceipt {
  if (session.status !== "boundary" || !session.boundaryId || !session.alignmentKey) {
    throw new Error("Receipt can only be created from a session paused at a boundary");
  }
  const lastSegment = session.segments[session.segments.length - 1];
  const top = session.stack[session.stack.length - 1];
  return {
    schema: "ps-migration/PrefixReceipt/v1",
    receiptId: args.receiptId,
    templateId: session.templateId,
    templateVersion: session.templateVersion,
    oldTemplateHash: args.oldTemplateHash,
    boundaryId: session.boundaryId,
    alignmentKey: session.alignmentKey,
    byteLength: session.byteLength,
    prefixSha256: prefixSha256(session.output),
    segments: session.segments.map((segment) => ({ ...segment })),
    finalContextId: top?.contextId ?? lastSegment?.contextId ?? "application/x-raw",
    finalEncoderRef: top?.encoderRef,
    openCallSiteIds: session.stack
      .slice(0, -1)
      .map((frame) => frame.callSiteNodeId)
      .filter((id): id is string => !!id),
    stackSummarySha256: args.stackSummaryHash,
    issuedAt: args.issuedAt,
    nonce: args.nonce,
  };
}

export interface SummarizeSessionOptions {
  summaryId: string;
  oldTemplateHash: string;
  complete?: boolean;
  incompletenessReasons?: string[];
  obligationByKind?: Record<string, Partial<Pick<LiveFrame, "contextId" | "encoderRef" | "activeBindingIds" | "requiredCloseTokens">>>;
}

export function buildStackSummaryFromSession(
  session: RenderSession,
  options: SummarizeSessionOptions,
): StackSummary {
  if (session.status !== "boundary" || !session.boundaryId || !session.alignmentKey) {
    throw new Error("Stack summary can only be built from a session paused at a boundary");
  }

  const frames: StackSummary["frames"] = {};
  for (const frame of session.stack) {
    const override = options.obligationByKind?.[frame.kindId];
    frames[frame.kindId] = {
      id: frame.kindId,
      fragmentId: frame.fragmentId,
      resumeNodeId: frame.resumeNodeId,
      callSiteNodeId: frame.callSiteNodeId,
      returnPointNodeId: frame.returnPointNodeId,
      contextId: override?.contextId ?? frame.contextId,
      encoderRef: override?.encoderRef ?? frame.encoderRef,
      activeBindingIds: override?.activeBindingIds ?? frame.activeBindingIds,
      requiredCloseTokens: override?.requiredCloseTokens ?? frame.requiredCloseTokens,
    };
  }

  const edges: StackSummary["edges"] = [];
  for (let index = 0; index < session.stack.length - 1; index += 1) {
    const caller = session.stack[index];
    const callee = session.stack[index + 1];
    if (!caller.callSiteNodeId) continue;
    const edge = {
      callerFrameId: caller.kindId,
      callSiteNodeId: caller.callSiteNodeId,
      calleeFrameId: callee.kindId,
    };
    if (!edges.some((existing) => JSON.stringify(existing) === JSON.stringify(edge))) {
      edges.push(edge);
    }
  }

  return {
    schema: SCHEMA.stackSummary,
    summaryId: options.summaryId,
    oldTemplateHash: options.oldTemplateHash,
    boundaryId: session.boundaryId,
    alignmentKey: session.alignmentKey,
    rootFrameId: session.stack[0].kindId,
    possibleTopFrameIds: [session.stack[session.stack.length - 1].kindId],
    frames,
    edges,
    complete: options.complete ?? true,
    incompletenessReasons: options.incompletenessReasons ?? [],
  };
}

export interface MigrateAndContinueArgs extends Omit<RenderArgs, "template"> {
  session: RenderSession;
  verification: MigrationVerificationResult;
  newTemplate: TemplateDocument;
}

export function migrateAndContinue(args: MigrateAndContinueArgs): RenderSession {
  const { session, verification, newTemplate } = args;
  if (!verification.canMigrate || !verification.safePlan) {
    session.status = "quarantined";
    throw new Error(
      `Refusing migration: verdict ${verification.verdict}; ${verification.findings
        .map((f) => f.code)
        .join(", ")}`,
    );
  }

  for (const frame of session.stack) {
    const continuation = verification.safePlan.continuations[frame.kindId];
    if (!continuation) {
      session.status = "quarantined";
      throw new Error(`Missing safe continuation for live frame ${frame.kindId}`);
    }
    const fragment = newTemplate.fragments[continuation.newFragmentId];
    const index = fragment?.nodes.findIndex((node) => node.id === continuation.newResumeNodeId) ?? -1;
    if (!fragment || index < 0) {
      session.status = "failed";
      throw new Error(`New continuation node missing: ${continuation.newResumeNodeId}`);
    }
    frame.fragmentId = continuation.newFragmentId;
    frame.pc = index;
    frame.resumeNodeId = continuation.newResumeNodeId;
    frame.contextId = continuation.contextId;
    frame.encoderRef = continuation.encoderRef;
    if (continuation.obligationOverrides?.requiredCloseTokens) {
      frame.requiredCloseTokens = continuation.obligationOverrides.requiredCloseTokens;
    }
    if (continuation.obligationOverrides?.activeBindingIds) {
      frame.activeBindingIds = continuation.obligationOverrides.activeBindingIds;
    }
  }

  session.templateId = newTemplate.id;
  session.templateVersion = newTemplate.version;
  session.status = "running";
  appendEvent(session, "migrate", undefined, {
    certificateHash: verification.certificateHash,
    depth: session.stack.length,
  });

  stepUntil({
    session,
    template: newTemplate,
    encoders: args.encoders,
    data: args.data,
    stopAtFirstBoundary: false,
    maxSteps: args.maxSteps ?? 100_000,
  });
  return session;
}

export interface ExperimentalContinueArgs extends RenderArgs {
  session: RenderSession;
  continuations: NonNullable<MigrationVerificationResult["safePlan"]>["continuations"];
}

export function experimentalContinueWithContinuations(args: ExperimentalContinueArgs): RenderSession {
  const { session, continuations, template } = args;
  for (const frame of session.stack) {
    const continuation = continuations[frame.kindId];
    if (!continuation) throw new Error(`Experiment missing continuation for ${frame.kindId}`);
    const fragment = template.fragments[continuation.newFragmentId];
    const index = fragment?.nodes.findIndex((node) => node.id === continuation.newResumeNodeId) ?? -1;
    if (!fragment || index < 0) throw new Error("Experiment continuation node missing");
    frame.fragmentId = continuation.newFragmentId;
    frame.pc = index;
    frame.resumeNodeId = continuation.newResumeNodeId;
    frame.contextId = continuation.contextId;
    frame.encoderRef = continuation.encoderRef;
  }
  session.status = "running";
  appendEvent(session, "migrate", undefined, { experimental: true });
  stepUntil({
    session,
    template: args.template,
    encoders: args.encoders,
    data: args.data,
    stopAtFirstBoundary: false,
    maxSteps: args.maxSteps ?? 100_000,
  });
  return session;
}
