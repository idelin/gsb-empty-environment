import { createHmac } from "node:crypto";
import {
  deduplicateFindings,
  hashCanonical,
  SCHEMA,
  verifyFrozen,
  type Finding,
  type Frozen,
  type Verdict,
} from "./core.ts";
import { verifyEncoderBinding } from "./encoders.ts";
import { auditReceipt } from "./receipts.ts";
import {
  checkStackReceiptCompatibility,
  detectCycleFrameIds,
  relevantFrameIds,
  stackSummaryHash,
  validateStackSummary,
} from "./stack.ts";
import {
  analyzeTail,
  closeTokensAppearInOrder,
  getNode,
  sourceLabelsCompatible,
  validateTemplate,
  validateTemplateEncoders,
} from "./templates.ts";
import type {
  AuthenticatedReceipt,
  EncoderSet,
  MigrationCertificate,
  ReceiptAuthenticator,
  StackSummary,
  TemplateDocument,
} from "./types.ts";

export interface VerifyMigrationOptions {
  certificate: MigrationCertificate;
  oldTemplate: Frozen<TemplateDocument>;
  newTemplate: Frozen<TemplateDocument>;
  oldEncoders: Frozen<EncoderSet>;
  newEncoders: Frozen<EncoderSet>;
  stackSummary: StackSummary;
  receipt: AuthenticatedReceipt;
  prefix?: string | Uint8Array;
  authenticator?: ReceiptAuthenticator;
  requireStackReceiptBinding?: boolean;
  certificateSecret?: string | Uint8Array;
}

export interface MigrationVerificationResult {
  verdict: Verdict;
  canMigrate: boolean;
  quarantined: boolean;
  certificateHash: string;
  findings: Finding[];
  relevantFrameIds: string[];
  safePlan?: {
    boundaryId: string;
    alignmentKey: string;
    continuations: MigrationCertificate["continuations"];
    encoderBindings: MigrationCertificate["encoderBindings"];
  };
}

export function certificateHash(certificate: MigrationCertificate): string {
  return hashCanonical(certificate);
}

export function signCertificate(certificate: MigrationCertificate, secret: string | Uint8Array): MigrationCertificate {
  const certificateMac = createHmac("sha256", secret)
    .update(hashCanonical(certificate))
    .digest("hex");
  return { ...certificate, issuerKeyId: "migration-authority-v1", certificateMac };
}

export function verifyMigration(options: VerifyMigrationOptions): MigrationVerificationResult {
  try {
    return verifyMigrationInner(options);
  } catch (error) {
    return {
      verdict: "quarantined",
      canMigrate: false,
      quarantined: true,
      certificateHash: safeCertificateHash(options?.certificate),
      findings: [
        {
          code: "VERIFIER_EXCEPTION",
          severity: "warning",
          message: "Verifier lacked or could not parse required information; instance is isolated",
          evidence: {
            error: error instanceof Error ? error.message : String(error),
          },
        },
      ],
      relevantFrameIds: [],
    };
  }
}

function safeCertificateHash(certificate: MigrationCertificate | undefined): string {
  try {
    return certificate ? certificateHash(certificate) : hashCanonical(null);
  } catch {
    return hashCanonical(null);
  }
}

function verifyMigrationInner(options: VerifyMigrationOptions): MigrationVerificationResult {
  const findings: Finding[] = [];
  const { certificate } = options;
  const certHash = certificateHash(certificate);

  if (certificate.schema !== SCHEMA.certificate) {
    findings.push({
      code: "CERT_SCHEMA_INVALID",
      severity: "error",
      message: "Migration certificate has an unsupported schema",
    });
  }

  for (const [label, frozen] of [
    ["old template", options.oldTemplate],
    ["new template", options.newTemplate],
    ["old encoder set", options.oldEncoders],
    ["new encoder set", options.newEncoders],
  ] as const) {
    if (!verifyFrozen(frozen)) {
      findings.push({
        code: "FROZEN_INPUT_INVALID",
        severity: "error",
        message: `Frozen ${label} failed hash verification`,
      });
    }
  }
  if (findings.some((finding) => finding.code === "FROZEN_INPUT_INVALID")) {
    const uniqueFindings = deduplicateFindings(findings);
    return {
      verdict: "denied",
      canMigrate: false,
      quarantined: false,
      certificateHash: certHash,
      findings: uniqueFindings,
      relevantFrameIds: [],
    };
  }

  const oldTemplate = options.oldTemplate.value;
  const newTemplate = options.newTemplate.value;
  const oldEncoders = options.oldEncoders.value;
  const newEncoders = options.newEncoders.value;
  const summary = options.stackSummary;
  const receipt = options.receipt;

  findings.push(...validateTemplate(oldTemplate).map(prefixFinding("old:")));
  findings.push(...validateTemplate(newTemplate).map(prefixFinding("new:")));
  findings.push(...validateTemplateEncoders(oldTemplate, oldEncoders).map(prefixFinding("old:")));
  findings.push(...validateTemplateEncoders(newTemplate, newEncoders).map(prefixFinding("new:")));
  findings.push(...validateStackSummary(summary, oldTemplate));

  const summaryHash = stackSummaryHash(summary);
  const receiptFindings = auditReceipt(receipt, {
    prefix: options.prefix,
    authenticator: options.authenticator,
    expectedOldTemplateHash: options.oldTemplate.sha256,
    expectedStackSummaryHash:
      options.requireStackReceiptBinding === false ? undefined : summaryHash,
  });
  findings.push(...receiptFindings);

  if (options.requireStackReceiptBinding !== false && !receipt.stackSummarySha256) {
    findings.push({
      code: "MIGRATION_STACK_COMMITMENT_MISSING",
      severity: "warning",
      message: "Receipt does not authenticate the stack-summary commitment; isolate the instance",
    });
  }

  findings.push(...checkStackReceiptCompatibility(summary, receipt));
  checkReceiptProvenance(options, findings);

  checkInputHashes(options, summaryHash, findings);
  checkBoundaryAlignment(options, findings);
  checkCertificateMac(certificate, options.certificateSecret, findings);

  const frameIds = relevantFrameIds(summary);
  checkUniversalProof(certificate, summary, frameIds, findings);
  checkEncoderBindings(certificate, oldEncoders, newEncoders, findings);
  checkFrameContinuations(options, frameIds, findings);
  checkReturnPointMerges(options, findings);

  const uniqueFindings = deduplicateFindings(findings);
  const verdict = decideVerdict(uniqueFindings);
  return {
    verdict,
    canMigrate: verdict === "safe",
    quarantined: verdict === "quarantined",
    certificateHash: certHash,
    findings: uniqueFindings,
    relevantFrameIds: frameIds,
    safePlan:
      verdict === "safe"
        ? {
            boundaryId: certificate.boundaryId,
            alignmentKey: certificate.alignmentKey,
            continuations: certificate.continuations,
            encoderBindings: certificate.encoderBindings,
          }
        : undefined,
  };
}

function prefixFinding(prefix: string): (finding: Finding) => Finding {
  return (finding) => ({ ...finding, message: `${prefix} ${finding.message}` });
}

function decideVerdict(findings: Finding[]): Verdict {
  if (findings.some((finding) => finding.severity === "error")) return "denied";
  if (findings.some((finding) => finding.severity === "warning")) return "quarantined";
  return "safe";
}

function checkReceiptProvenance(options: VerifyMigrationOptions, findings: Finding[]): void {
  const { receipt, oldTemplate, oldEncoders } = options;
  for (const segment of receipt.segments) {
    const node = getNode(oldTemplate.value, segment.fragmentId, segment.nodeId);
    if (!node) {
      findings.push({
        code: "RECEIPT_NODE_NOT_IN_FROZEN_TEMPLATE",
        severity: "error",
        message: `Receipt segment ${segment.nodeId} cannot be traced to the frozen old template`,
      });
      continue;
    }

    if (segment.kind === "field") {
      if (node.kind !== "field") {
        findings.push({
          code: "RECEIPT_FIELD_NODE_KIND_MISMATCH",
          severity: "error",
          message: `Receipt field segment ${segment.nodeId} is not a field in frozen template`,
        });
        continue;
      }
      if (segment.bindingId !== node.bindingId) {
        findings.push({
          code: "RECEIPT_BINDING_MISMATCH",
          severity: "error",
          message: `Receipt segment ${segment.nodeId} changes source binding identity`,
        });
      }
      if (!segment.sourceLabel || !sourceLabelsCompatible(node.sourceLabel, segment.sourceLabel)) {
        findings.push({
          code: "RECEIPT_SOURCE_LABEL_MISMATCH",
          severity: "error",
          message: `Receipt segment ${segment.nodeId} does not preserve its source label`,
        });
      }
      if (segment.contextId !== node.contextId) {
        findings.push({
          code: "RECEIPT_CONTEXT_MISMATCH",
          severity: "error",
          message: `Receipt segment ${segment.nodeId} context differs from frozen template authorization`,
        });
      }
      if (segment.encoderId !== node.encoderId) {
        findings.push({
          code: "RECEIPT_ENCODER_MISMATCH",
          severity: "error",
          message: `Receipt segment ${segment.nodeId} uses an unexpected encoder`,
        });
      }
      const table = oldEncoders.value[node.encoderId];
      const stateId = segment.encoderStateBefore ?? node.stateId ?? table?.initialStateId;
      if (!table || !stateId || !table.states[stateId]) {
        findings.push({
          code: "RECEIPT_ENCODER_STATE_UNTRACEABLE",
          severity: "error",
          message: `Receipt segment ${segment.nodeId} has an unknown pre-migration encoder state`,
        });
      }
    } else if (node.kind !== "literal") {
      findings.push({
        code: "RECEIPT_LITERAL_NODE_KIND_MISMATCH",
        severity: "error",
        message: `Receipt literal segment ${segment.nodeId} is not a literal in frozen template`,
      });
    }
  }
}

function checkInputHashes(
  options: VerifyMigrationOptions,
  summaryHash: string,
  findings: Finding[],
): void {
  const claims = options.certificate.inputHashes;
  const expected = {
    oldTemplate: options.oldTemplate.sha256,
    newTemplate: options.newTemplate.sha256,
    oldEncoders: options.oldEncoders.sha256,
    newEncoders: options.newEncoders.sha256,
    stackSummary: summaryHash,
    receipt: hashCanonical(options.receipt),
  };

  for (const key of Object.keys(expected) as Array<keyof typeof expected>) {
    if (claims[key] !== expected[key]) {
      findings.push({
        code: "CERT_INPUT_HASH_MISMATCH",
        severity: "error",
        message: `Certificate ${key} hash does not match supplied frozen input`,
        evidence: { key, claimed: claims[key], expected: expected[key] },
      });
    }
  }
}

function checkBoundaryAlignment(options: VerifyMigrationOptions, findings: Finding[]): void {
  const { certificate, oldTemplate, newTemplate, receipt, stackSummary: summary } = options;
  if (certificate.boundaryId !== receipt.boundaryId || certificate.boundaryId !== summary.boundaryId) {
    findings.push({
      code: "MIGRATION_BOUNDARY_NOT_ALIGNED",
      severity: "error",
      message: "Certificate, receipt, and stack summary disagree on the migration boundary",
    });
  }
  if (certificate.alignmentKey !== receipt.alignmentKey || certificate.alignmentKey !== summary.alignmentKey) {
    findings.push({
      code: "MIGRATION_ALIGNMENT_KEY_MISMATCH",
      severity: "error",
      message: "Certificate alignment key does not match authenticated receipt",
    });
  }

  const oldBoundary = findBoundary(oldTemplate.value, certificate.boundaryId);
  if (!oldBoundary || oldBoundary.kind !== "boundary") {
    findings.push({
      code: "OLD_BOUNDARY_MISSING",
      severity: "error",
      message: "Old frozen template does not contain the claimed boundary",
    });
  } else if (oldBoundary.alignmentKey !== certificate.alignmentKey) {
    findings.push({
      code: "OLD_BOUNDARY_ALIGNMENT_INVALID",
      severity: "error",
      message: "Old boundary alignment key is invalid",
    });
  }

  const newBoundary = findBoundaryByAlignment(newTemplate.value, certificate.alignmentKey);
  if (!newBoundary) {
    findings.push({
      code: "NEW_ALIGNED_BOUNDARY_MISSING",
      severity: "error",
      message: "New frozen template has no output boundary with the same alignment key",
    });
  }
}

function findBoundary(template: TemplateDocument, boundaryId: string) {
  for (const fragment of Object.values(template.fragments)) {
    const node = fragment.nodes.find(
      (candidate) => candidate.kind === "boundary" && candidate.boundaryId === boundaryId,
    );
    if (node) return node;
  }
  return undefined;
}

function findBoundaryByAlignment(template: TemplateDocument, alignmentKey: string) {
  for (const fragment of Object.values(template.fragments)) {
    const node = fragment.nodes.find(
      (candidate) => candidate.kind === "boundary" && candidate.alignmentKey === alignmentKey,
    );
    if (node) return node;
  }
  return undefined;
}

function checkCertificateMac(
  certificate: MigrationCertificate,
  secret: string | Uint8Array | undefined,
  findings: Finding[],
): void {
  if (!certificate.certificateMac) return;
  if (!secret) {
    findings.push({
      code: "CERT_ISSUER_KEY_MISSING",
      severity: "warning",
      message: "Certificate is signed but no issuer key was supplied for verification",
    });
    return;
  }
  const expected = createHmac("sha256", secret).update(hashCanonical(certificate)).digest("hex");
  if (expected !== certificate.certificateMac) {
    findings.push({
      code: "CERT_MAC_INVALID",
      severity: "error",
      message: "Certificate issuer authentication failed",
    });
  }
}

function checkUniversalProof(
  certificate: MigrationCertificate,
  summary: StackSummary,
  relevantFrameIds: string[],
  findings: Finding[],
): void {
  const claimed = [...certificate.universalProof.relevantFrameIds].sort();
  const actual = [...relevantFrameIds].sort();
  if (claimed.join("\u0000") !== actual.join("\u0000")) {
    findings.push({
      code: "CERT_UNIVERSAL_FRAME_SET_STALE",
      severity: "warning",
      message: "Certificate universal frame set does not exactly match recomputed relevant stack graph",
      evidence: { claimed, actual },
    });
  }

  const actualCycles = detectCycleFrameIds(summary);
  const claimedCycles = [...certificate.universalProof.cycleInvariantFrameIds].sort();
  if (claimedCycles.join("\u0000") !== actualCycles.join("\u0000")) {
    findings.push({
      code: "CERT_CYCLE_INVARIANT_SET_STALE",
      severity: "warning",
      message: "Certificate recursive-cycle set does not match recomputed stack graph",
      evidence: { claimedCycles, actualCycles },
    });
  }

  if (certificate.universalProof.receiptCompatibility !== "bound-hash-and-context/v1") {
    findings.push({
      code: "CERT_RECEIPT_COMPATIBILITY_WEAK",
      severity: "warning",
      message: "Certificate uses context-only receipt compatibility and cannot establish stack completeness",
    });
  }
}

function checkEncoderBindings(
  certificate: MigrationCertificate,
  oldEncoders: EncoderSet,
  newEncoders: EncoderSet,
  findings: Finding[],
): void {
  for (const binding of certificate.encoderBindings) {
    findings.push(...verifyEncoderBinding(binding, oldEncoders, newEncoders));
  }
}

function checkFrameContinuations(
  options: VerifyMigrationOptions,
  relevantFrameIds: string[],
  findings: Finding[],
): void {
  const { certificate, stackSummary: summary, oldTemplate, newTemplate, oldEncoders, newEncoders } = options;

  for (const frameId of relevantFrameIds) {
    const frame = summary.frames[frameId];
    const continuation = certificate.continuations[frameId];
    if (!continuation) {
      findings.push({
        code: "CERT_FRAME_CONTINUATION_MISSING",
        severity: "warning",
        message: `No continuation proof for stack frame ${frameId}; deep return obligation cannot be discharged`,
        evidence: { frameId },
      });
      continue;
    }

    const newNode = getNode(newTemplate.value, continuation.newFragmentId, continuation.newResumeNodeId);
    if (!newNode) {
      findings.push({
        code: "CERT_NEW_RESUME_NODE_MISSING",
        severity: "error",
        message: `Continuation for frame ${frameId} references missing new node`,
        evidence: { continuation },
      });
      continue;
    }

    if (frame.contextId !== continuation.contextId) {
      findings.push({
        code: "FRAME_CONTEXT_RENAMED",
        severity: "error",
        message: `Frame ${frameId} continuation changes lexical context without an aligned transition`,
        evidence: {
          oldContextId: frame.contextId,
          newContextId: continuation.contextId,
        },
      });
    }

    const effectiveActiveBindingIds =
      continuation.obligationOverrides?.activeBindingIds ?? frame.activeBindingIds;
    const effectiveRequiredCloseTokens =
      continuation.obligationOverrides?.requiredCloseTokens ?? frame.requiredCloseTokens;
    if (
      canonicalSorted(effectiveActiveBindingIds) !==
      canonicalSorted(frame.activeBindingIds)
    ) {
      findings.push({
        code: "FRAME_BINDING_OBLIGATION_CHANGED",
        severity: "error",
        message: `Frame ${frameId} changes active source-binding obligations`,
      });
    }
    if (
      JSON.stringify(effectiveRequiredCloseTokens) !==
      JSON.stringify(frame.requiredCloseTokens)
    ) {
      findings.push({
        code: "FRAME_CLOSE_OBLIGATION_CHANGED",
        severity: "error",
        message: `Frame ${frameId} changes ordered close-token obligations`,
        evidence: {
          old: frame.requiredCloseTokens,
          next: effectiveRequiredCloseTokens,
        },
      });
    }

    if (frame.encoderRef) {
      const binding = certificate.encoderBindings.find(
        (candidate) =>
          candidate.oldEncoderId === frame.encoderRef!.encoderId &&
          candidate.oldStateId === frame.encoderRef!.stateId,
      );
      if (!binding) {
        findings.push({
          code: "FRAME_ENCODER_BINDING_MISSING",
          severity: "warning",
          message: `Frame ${frameId} does not bind its pre-migration encoder state`,
          evidence: { encoderRef: frame.encoderRef },
        });
      } else if (
        !continuation.encoderRef ||
        continuation.encoderRef.encoderId !== binding.newEncoderId ||
        continuation.encoderRef.stateId !== binding.newStateId
      ) {
        findings.push({
          code: "FRAME_ENCODER_CONTINUATION_MISMATCH",
          severity: "error",
          message: `Frame ${frameId} continuation encoder does not match certified binding`,
        });
      }
    }

    const oldTail = analyzeTail({
      template: oldTemplate.value,
      encoders: oldEncoders.value,
      fragmentId: frame.fragmentId,
      nodeId: frame.resumeNodeId,
      initialContextId: frame.contextId,
      initialEncoder: frame.encoderRef,
    });
    const newTail = analyzeTail({
      template: newTemplate.value,
      encoders: newEncoders.value,
      fragmentId: continuation.newFragmentId,
      nodeId: continuation.newResumeNodeId,
      initialContextId: continuation.contextId,
      initialEncoder: continuation.encoderRef,
    });
    findings.push(...oldTail.findings.map(prefixFinding(`old-tail:${frameId}:`)));
    findings.push(...newTail.findings.map(prefixFinding(`new-tail:${frameId}:`)));

    if (!closeTokensAppearInOrder(oldTail.literalValues, frame.requiredCloseTokens)) {
      findings.push({
        code: "OLD_TAIL_CLOSE_OBLIGATION_UNMET",
        severity: "error",
        message: `Old template tail for frame ${frameId} cannot discharge required close tokens`,
      });
    }
    if (!closeTokensAppearInOrder(newTail.literalValues, frame.requiredCloseTokens)) {
      findings.push({
        code: "NEW_TAIL_CLOSE_OBLIGATION_UNMET",
        severity: "error",
        message: `New continuation for deep frame ${frameId} drops a return/close obligation`,
        evidence: { requiredCloseTokens: frame.requiredCloseTokens, literals: newTail.literalValues },
      });
    }

    compareTailFields(frameId, oldTail, newTail, options, findings);
  }
}

function compareTailFields(
  frameId: string,
  oldTail: ReturnType<typeof analyzeTail>,
  newTail: ReturnType<typeof analyzeTail>,
  options: VerifyMigrationOptions,
  findings: Finding[],
): void {
  const newByBinding = new Map<string, typeof newTail.fields>();
  for (const field of newTail.fields) {
    const list = newByBinding.get(field.bindingId) ?? [];
    list.push(field);
    newByBinding.set(field.bindingId, list);
  }

  for (const oldField of oldTail.fields) {
    const candidates = newByBinding.get(oldField.bindingId);
    if (!candidates || candidates.length === 0) {
      findings.push({
        code: "TAIL_BINDING_MISSING_IN_NEW_VERSION",
        severity: "error",
        message: `Deep continuation for frame ${frameId} loses source binding ${oldField.bindingId}`,
      });
      continue;
    }

    const matching = candidates.find((newField) => {
      if (!sourceLabelsCompatible(oldField.sourceLabel, newField.sourceLabel)) return false;
      if (oldField.contextId !== newField.contextId) return false;
      if (!newField.authorizedContextIds.includes(oldField.contextId)) return false;
      return encoderFieldCompatible(oldField, newField, options);
    });

    if (!matching) {
      findings.push({
        code: "TAIL_FIELD_SECURITY_CONTEXT_CHANGED",
        severity: "error",
        message: `Binding ${oldField.bindingId} no longer preserves source label, lexical authorization, or encoder context`,
        evidence: { oldField, newCandidates: candidates },
      });
    }
  }
}

function encoderFieldCompatible(
  oldField: { encoderId?: string; encoderStateId?: string; contextId: string },
  newField: { encoderId?: string; encoderStateId?: string; contextId: string },
  options: VerifyMigrationOptions,
): boolean {
  if (!oldField.encoderId || !newField.encoderId) return oldField.encoderId === newField.encoderId;
  const oldTable = options.oldEncoders.value[oldField.encoderId];
  const newTable = options.newEncoders.value[newField.encoderId];
  if (!oldTable || !newTable) return false;
  const oldState = oldField.encoderStateId ?? oldTable.initialStateId;
  const newState = newField.encoderStateId ?? newTable.initialStateId;
  const binding = options.certificate.encoderBindings.find(
    (candidate) =>
      candidate.oldEncoderId === oldField.encoderId &&
      candidate.oldStateId === oldState &&
      candidate.newEncoderId === newField.encoderId &&
      candidate.newStateId === newState,
  );
  if (!binding) return false;
  return true;
}

function checkReturnPointMerges(options: VerifyMigrationOptions, findings: Finding[]): void {
  const { certificate, oldTemplate, newTemplate, stackSummary: summary } = options;
  for (const merge of certificate.returnPointMerges) {
    const newNode = findNodeGlobal(newTemplate.value, merge.newReturnPointNodeId);
    if (!newNode) {
      findings.push({
        code: "MERGE_NEW_RETURN_POINT_MISSING",
        severity: "error",
        message: `Return merge ${merge.mergeGroupId} points to a missing new node`,
      });
    }

    for (const oldReturnPointId of merge.oldReturnPointNodeIds) {
      const oldNode = findNodeGlobal(oldTemplate.value, oldReturnPointId);
      if (!oldNode) {
        findings.push({
          code: "MERGE_OLD_RETURN_POINT_MISSING",
          severity: "error",
          message: `Return merge ${merge.mergeGroupId} references missing old return point ${oldReturnPointId}`,
        });
      }

      const frames = Object.values(summary.frames).filter(
        (frame) => frame.returnPointNodeId === oldReturnPointId,
      );
      if (frames.length === 0) {
        findings.push({
          code: "MERGE_RETURN_POINT_NOT_IN_STACK_SUMMARY",
          severity: "warning",
          message: `Merged return point ${oldReturnPointId} is not present in any stack frame proof`,
        });
      }
      for (const frame of frames) {
        const continuation = certificate.continuations[frame.id];
        if (continuation?.newResumeNodeId !== merge.newReturnPointNodeId) {
          findings.push({
            code: "MERGE_CONTINUATION_INCONSISTENT",
            severity: "error",
            message: `Frame ${frame.id} claims return merge ${merge.mergeGroupId} but continuation differs`,
          });
        }
        if (continuation?.mergeGroupId !== merge.mergeGroupId) {
          findings.push({
            code: "MERGE_GROUP_NOT_TAGGED",
            severity: "warning",
            message: `Frame ${frame.id} continuation is not tagged with its certified merge group`,
          });
        }
      }
    }
  }
}

function findNodeGlobal(template: TemplateDocument, nodeId: string) {
  for (const fragment of Object.values(template.fragments)) {
    const node = fragment.nodes.find((candidate) => candidate.id === nodeId);
    if (node) return node;
  }
  return undefined;
}

function canonicalSorted(values: string[]): string {
  return JSON.stringify([...values].sort());
}

export interface IssueCertificateArgs {
  certificateId: string;
  boundaryId: string;
  alignmentKey: string;
  oldTemplate: Frozen<TemplateDocument>;
  newTemplate: Frozen<TemplateDocument>;
  oldEncoders: Frozen<EncoderSet>;
  newEncoders: Frozen<EncoderSet>;
  stackSummary: StackSummary;
  receipt: AuthenticatedReceipt;
  continuations: MigrationCertificate["continuations"];
  encoderBindings: MigrationCertificate["encoderBindings"];
  returnPointMerges?: MigrationCertificate["returnPointMerges"];
  issuedAt?: string;
  receiptCompatibility?: MigrationCertificate["universalProof"]["receiptCompatibility"];
}

export function issueCertificate(args: IssueCertificateArgs): MigrationCertificate {
  const frameIds = relevantFrameIds(args.stackSummary);
  const cycleInvariantFrameIds = detectCycleFrameIds(args.stackSummary);
  return {
    schema: SCHEMA.certificate,
    certificateId: args.certificateId,
    boundaryId: args.boundaryId,
    alignmentKey: args.alignmentKey,
    inputHashes: {
      oldTemplate: args.oldTemplate.sha256,
      newTemplate: args.newTemplate.sha256,
      oldEncoders: args.oldEncoders.sha256,
      newEncoders: args.newEncoders.sha256,
      stackSummary: stackSummaryHash(args.stackSummary),
      receipt: hashCanonical(args.receipt),
    },
    continuations: args.continuations,
    encoderBindings: args.encoderBindings,
    returnPointMerges: args.returnPointMerges ?? [],
    universalProof: {
      summaryKind: "regular-stack-graph/v1",
      relevantFrameIds: frameIds,
      cycleInvariantFrameIds,
      receiptCompatibility: args.receiptCompatibility ?? "bound-hash-and-context/v1",
    },
    issuedAt: args.issuedAt,
  };
}
