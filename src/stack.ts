import { hashCanonical, type Finding } from "./core.ts";
import { getNode } from "./templates.ts";
import type { AuthenticatedReceipt, StackSummary, TemplateDocument } from "./types.ts";

export function stackSummaryHash(summary: StackSummary): string {
  return hashCanonical(summary);
}

export function validateStackSummary(
  summary: StackSummary,
  oldTemplate?: TemplateDocument,
): Finding[] {
  const findings: Finding[] = [];

  if (!summary || typeof summary !== "object") {
    return [
      { code: "STACK_SUMMARY_INVALID", severity: "error", message: "Stack summary is invalid" },
    ];
  }

  if (!summary.complete) {
    findings.push({
      code: "STACK_SUMMARY_INCOMPLETE",
      severity: "warning",
      message: "Stack summary does not describe every return obligation; instance must be isolated",
      evidence: { reasons: summary.incompletenessReasons },
    });
  }

  if (summary.incompletenessReasons.length > 0) {
    findings.push({
      code: "STACK_SUMMARY_MISSING_INFORMATION",
      severity: "warning",
      message: "Stack summary reports missing information",
      evidence: { reasons: summary.incompletenessReasons },
    });
  }

  if (!summary.frames[summary.rootFrameId]) {
    findings.push({
      code: "STACK_ROOT_FRAME_MISSING",
      severity: "error",
      message: "Stack summary root frame is missing",
    });
  }

  for (const rootId of summary.rootFrameIds ?? []) {
    if (!summary.frames[rootId]) {
      findings.push({
        code: "STACK_ROOT_FRAME_MISSING",
        severity: "error",
        message: `Alternative root frame ${rootId} is missing`,
      });
    }
  }

  for (const frame of Object.values(summary.frames)) {
    if (!frame.contextId) {
      findings.push({
        code: "STACK_FRAME_CONTEXT_MISSING",
        severity: "warning",
        message: `Frame ${frame.id} has no lexical context`,
      });
    }
    if (oldTemplate) {
      const node = getNode(oldTemplate, frame.fragmentId, frame.resumeNodeId);
      if (!node) {
        findings.push({
          code: "STACK_FRAME_RESUME_NODE_MISSING",
          severity: "error",
          message: `Frame ${frame.id} resumes at missing old node ${frame.fragmentId}::${frame.resumeNodeId}`,
        });
      }
      if (frame.returnPointNodeId) {
        const returnNode = getNode(oldTemplate, frame.fragmentId, frame.returnPointNodeId);
        if (!returnNode) {
          findings.push({
            code: "STACK_RETURN_POINT_MISSING",
            severity: "error",
            message: `Frame ${frame.id} references missing return point ${frame.returnPointNodeId}`,
          });
        }
      }
    }
  }

  for (const topId of summary.possibleTopFrameIds) {
    if (!summary.frames[topId]) {
      findings.push({
        code: "STACK_TOP_FRAME_MISSING",
        severity: "error",
        message: `Possible top frame ${topId} is missing`,
      });
    }
  }

  for (const edge of summary.edges) {
    if (!summary.frames[edge.callerFrameId] || !summary.frames[edge.calleeFrameId]) {
      findings.push({
        code: "STACK_EDGE_ENDPOINT_MISSING",
        severity: "error",
        message: "Stack edge references a missing frame",
        evidence: { edge },
      });
    }
  }

  const relevant = relevantFrameIds(summary);
  for (const frameId of relevant) {
    if (!summary.frames[frameId]) {
      findings.push({
        code: "STACK_RELEVANT_FRAME_MISSING",
        severity: "error",
        message: `Relevant frame ${frameId} is absent`,
      });
    }
  }

  return findings;
}

export function reachableFrameIds(summary: StackSummary, start = summary.rootFrameId): Set<string> {
  const result = new Set<string>();
  if (!summary.frames[start]) return result;
  const queue = [start];
  while (queue.length) {
    const id = queue.shift()!;
    if (result.has(id)) continue;
    result.add(id);
    for (const edge of summary.edges.filter((e) => e.callerFrameId === id)) {
      queue.push(edge.calleeFrameId);
    }
  }
  return result;
}

export function canReachTopFrameIds(summary: StackSummary): Set<string> {
  const reverseEdges = new Map<string, string[]>();
  for (const edge of summary.edges) {
    const list = reverseEdges.get(edge.calleeFrameId) ?? [];
    list.push(edge.callerFrameId);
    reverseEdges.set(edge.calleeFrameId, list);
  }

  const result = new Set<string>();
  const queue = [...summary.possibleTopFrameIds];
  while (queue.length) {
    const id = queue.shift()!;
    if (result.has(id)) continue;
    result.add(id);
    for (const caller of reverseEdges.get(id) ?? []) queue.push(caller);
  }
  return result;
}

export function relevantFrameIds(summary: StackSummary): string[] {
  const rootIds = summary.rootFrameIds?.length ? summary.rootFrameIds : [summary.rootFrameId];
  const reachable = new Set<string>();
  for (const rootId of rootIds) {
    for (const frameId of reachableFrameIds(summary, rootId)) reachable.add(frameId);
  }
  const canReachTop = canReachTopFrameIds(summary);
  return [...reachable].filter((id) => canReachTop.has(id)).sort();
}

export function findReceiptCallSitePath(
  summary: StackSummary,
  callSiteIds: string[],
): string[] | undefined {
  if (callSiteIds.length === 0) {
    return summary.possibleTopFrameIds.includes(summary.rootFrameId)
      ? [summary.rootFrameId]
      : undefined;
  }

  const search = (frameId: string, edgeIndex: number, path: string[]): string[] | undefined => {
    if (edgeIndex === callSiteIds.length) {
      return summary.possibleTopFrameIds.includes(frameId) ? path : undefined;
    }
    const expectedCallSite = callSiteIds[edgeIndex];
    for (const edge of summary.edges) {
      if (edge.callerFrameId !== frameId || edge.callSiteNodeId !== expectedCallSite) continue;
      const found = search(edge.calleeFrameId, edgeIndex + 1, [...path, edge.calleeFrameId]);
      if (found) return found;
    }
    return undefined;
  };

  const rootIds = summary.rootFrameIds?.length ? summary.rootFrameIds : [summary.rootFrameId];
  for (const rootId of rootIds) {
    const found = search(rootId, 0, [rootId]);
    if (found) return found;
  }
  return undefined;
}

export function checkStackReceiptCompatibility(
  summary: StackSummary,
  receipt: AuthenticatedReceipt,
): Finding[] {
  const findings: Finding[] = [];

  if (summary.oldTemplateHash !== receipt.oldTemplateHash) {
    findings.push({
      code: "STACK_RECEIPT_TEMPLATE_MISMATCH",
      severity: "error",
      message: "Stack summary and receipt refer to different old template hashes",
    });
  }
  if (summary.boundaryId !== receipt.boundaryId || summary.alignmentKey !== receipt.alignmentKey) {
    findings.push({
      code: "STACK_RECEIPT_BOUNDARY_MISMATCH",
      severity: "error",
      message: "Stack summary and receipt are not at the same aligned boundary",
    });
  }

  for (const topId of summary.possibleTopFrameIds) {
    const top = summary.frames[topId];
    if (!top) continue;
    if (top.contextId !== receipt.finalContextId) {
      findings.push({
        code: "STACK_RECEIPT_CONTEXT_INCOMPATIBLE",
        severity: "error",
        message: `Possible top frame ${topId} context ${top.contextId} differs from receipt context ${receipt.finalContextId}`,
      });
    }
    if (receipt.finalEncoderRef) {
      if (
        !top.encoderRef ||
        top.encoderRef.encoderId !== receipt.finalEncoderRef.encoderId ||
        top.encoderRef.stateId !== receipt.finalEncoderRef.stateId
      ) {
        findings.push({
          code: "STACK_RECEIPT_ENCODER_INCOMPATIBLE",
          severity: "error",
          message: `Possible top frame ${topId} encoder state is incompatible with receipt`,
          evidence: { topEncoder: top.encoderRef ?? null, receiptEncoder: receipt.finalEncoderRef },
        });
      }
    }
  }

  const path = findReceiptCallSitePath(summary, receipt.openCallSiteIds);
  if (!path) {
    findings.push({
      code: "STACK_RECEIPT_CALL_CHAIN_INCOMPATIBLE",
      severity: "error",
      message: "No root-to-top stack in the summary matches the receipt's authenticated call chain",
      evidence: { openCallSiteIds: receipt.openCallSiteIds },
    });
  }

  return findings;
}

export function detectCycleFrameIds(summary: StackSummary): string[] {
  const cyclic = new Set<string>();
  const state = new Map<string, 0 | 1 | 2>();

  const visit = (id: string, stack: Set<string>): void => {
    state.set(id, 1);
    stack.add(id);
    for (const edge of summary.edges.filter((e) => e.callerFrameId === id)) {
      if (stack.has(edge.calleeFrameId)) {
        cyclic.add(edge.calleeFrameId);
      } else if (state.get(edge.calleeFrameId) !== 2) {
        visit(edge.calleeFrameId, stack);
      }
    }
    stack.delete(id);
    state.set(id, 2);
  };

  if (summary.frames[summary.rootFrameId]) visit(summary.rootFrameId, new Set());
  return [...cyclic].sort();
}
