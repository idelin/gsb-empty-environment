import { hashCanonical, type Finding } from "./core.ts";
import { checkEncoderStatePolicy } from "./encoders.ts";
import type {
  ContextId,
  EncoderSet,
  FieldNode,
  TemplateDocument,
  TemplateNode,
} from "./types.ts";

export function templateHash(template: TemplateDocument): string {
  return hashCanonical(template);
}

export function nodeRef(fragmentId: string, nodeId: string): string {
  return `${fragmentId}::${nodeId}`;
}

export function getFragment(template: TemplateDocument, fragmentId: string) {
  return template.fragments[fragmentId];
}

export function getNode(
  template: TemplateDocument,
  fragmentId: string,
  nodeId: string,
): TemplateNode | undefined {
  return template.fragments[fragmentId]?.nodes.find((node) => node.id === nodeId);
}

export function getNodeIndex(
  template: TemplateDocument,
  fragmentId: string,
  nodeId: string,
): number {
  return template.fragments[fragmentId]?.nodes.findIndex((node) => node.id === nodeId) ?? -1;
}

export function validateTemplate(template: TemplateDocument): Finding[] {
  const findings: Finding[] = [];
  if (!template || typeof template !== "object") {
    return [
      { code: "TEMPLATE_INVALID", severity: "error", message: "Template is not an object" },
    ];
  }
  if (!template.fragments?.[template.entryFragmentId]) {
    findings.push({
      code: "TEMPLATE_ENTRY_MISSING",
      severity: "error",
      message: `Entry fragment ${template.entryFragmentId} is missing`,
    });
  }

  const globalNodeIds = new Map<string, string>();
  for (const fragment of Object.values(template.fragments ?? {})) {
    if (!Array.isArray(fragment.nodes)) {
      findings.push({
        code: "TEMPLATE_FRAGMENT_INVALID",
        severity: "error",
        message: `Fragment ${fragment.id} has no nodes array`,
      });
      continue;
    }

    const ids = new Set<string>();
    fragment.nodes.forEach((node, index) => {
      if (ids.has(node.id)) {
        findings.push({
          code: "TEMPLATE_DUPLICATE_NODE",
          severity: "error",
          message: `Duplicate node id ${node.id} in fragment ${fragment.id}`,
        });
      }
      ids.add(node.id);

      const globalKey = node.id;
      const previous = globalNodeIds.get(globalKey);
      if (previous && previous !== fragment.id) {
        findings.push({
          code: "TEMPLATE_NODE_ID_NOT_GLOBAL",
          severity: "warning",
          message: `Node id ${node.id} is reused in fragments ${previous} and ${fragment.id}`,
        });
      } else {
        globalNodeIds.set(globalKey, fragment.id);
      }

      if (node.kind === "call") {
        if (!template.fragments[node.targetFragmentId]) {
          findings.push({
            code: "TEMPLATE_CALL_TARGET_MISSING",
            severity: "error",
            message: `Call ${node.id} targets missing fragment ${node.targetFragmentId}`,
          });
        }
        const returnIndex = getNodeIndex(template, fragment.id, node.returnPointId);
        if (returnIndex < 0) {
          findings.push({
            code: "TEMPLATE_RETURN_POINT_MISSING",
            severity: "error",
            message: `Call ${node.id} has missing return point ${node.returnPointId}`,
          });
        } else if (returnIndex !== index + 1) {
          findings.push({
            code: "TEMPLATE_RETURN_POINT_NOT_ADJACENT",
            severity: "error",
            message: `Call ${node.id} return point must be the immediately following node`,
          });
        }
      }
    });
  }
  return findings;
}

export function validateTemplateEncoders(template: TemplateDocument, encoders: EncoderSet): Finding[] {
  const findings: Finding[] = [];
  for (const fragment of Object.values(template.fragments)) {
    for (const node of fragment.nodes) {
      if (node.kind !== "field") continue;
      const table = encoders[node.encoderId];
      if (!table) {
        findings.push({
          code: "TEMPLATE_ENCODER_MISSING",
          severity: "error",
          message: `Field ${node.id} references missing encoder ${node.encoderId}`,
        });
        continue;
      }
      const stateId = node.stateId ?? table.initialStateId;
      if (!table.states[stateId]) {
        findings.push({
          code: "TEMPLATE_ENCODER_STATE_MISSING",
          severity: "error",
          message: `Field ${node.id} references missing state ${node.encoderId}:${stateId}`,
        });
      } else {
        findings.push(...checkEncoderStatePolicy(table, stateId));
        if (table.states[stateId].contextId !== node.contextId) {
          findings.push({
            code: "TEMPLATE_FIELD_CONTEXT_ENCODER_MISMATCH",
            severity: "error",
            message: `Field ${node.id} context does not match encoder state context`,
            evidence: {
              nodeId: node.id,
              fieldContextId: node.contextId,
              encoderContextId: table.states[stateId].contextId,
            },
          });
        }
      }
      if (!node.authorizedContextIds.includes(node.contextId)) {
        findings.push({
          code: "TEMPLATE_FIELD_CONTEXT_UNAUTHORIZED",
          severity: "error",
          message: `Field ${node.bindingId} is not authorized for context ${node.contextId}`,
          evidence: { nodeId: node.id, authorized: node.authorizedContextIds },
        });
      }
    }
  }
  return findings;
}

export interface TailFieldObservation {
  ref: string;
  fragmentId: string;
  nodeId: string;
  bindingId: string;
  sourceLabel: string;
  contextId: ContextId;
  authorizedContextIds: ContextId[];
  encoderId?: string;
  encoderStateId?: string;
}

export interface TailReport {
  start: string;
  reachableNodeRefs: string[];
  fields: TailFieldObservation[];
  literalValues: string[];
  contextIds: ContextId[];
  cycleRefs: string[];
  findings: Finding[];
}

export interface AnalyzeTailOptions {
  template: TemplateDocument;
  encoders?: EncoderSet;
  fragmentId: string;
  nodeId: string;
  initialContextId: ContextId;
  initialEncoder?: { encoderId: string; stateId: string };
}

interface WorkItem {
  fragmentId: string;
  nodeIndex: number;
  contextId: ContextId;
  encoder?: { encoderId: string; stateId: string };
}

export function analyzeTail(options: AnalyzeTailOptions): TailReport {
  const { template, encoders } = options;
  const startIndex = getNodeIndex(template, options.fragmentId, options.nodeId);
  const report: TailReport = {
    start: nodeRef(options.fragmentId, options.nodeId),
    reachableNodeRefs: [],
    fields: [],
    literalValues: [],
    contextIds: [options.initialContextId],
    cycleRefs: [],
    findings: [],
  };

  if (startIndex < 0) {
    report.findings.push({
      code: "TAIL_START_MISSING",
      severity: "error",
      message: `Tail start ${nodeRef(options.fragmentId, options.nodeId)} is missing`,
    });
    return report;
  }

  const visited = new Set<string>();
  const queue: WorkItem[] = [
    {
      fragmentId: options.fragmentId,
      nodeIndex: startIndex,
      contextId: options.initialContextId,
      encoder: options.initialEncoder,
    },
  ];

  while (queue.length > 0) {
    const item = queue.shift()!;
    const fragment = template.fragments[item.fragmentId];
    if (!fragment) {
      report.findings.push({
        code: "TAIL_FRAGMENT_MISSING",
        severity: "error",
        message: `Fragment ${item.fragmentId} is missing`,
      });
      continue;
    }
    if (item.nodeIndex >= fragment.nodes.length) continue;

    const node = fragment.nodes[item.nodeIndex];
    const visitKey = `${item.fragmentId}:${node.id}:${item.contextId}:${item.encoder?.encoderId ?? "-"}:${item.encoder?.stateId ?? "-"}`;
    if (visited.has(visitKey)) {
      report.cycleRefs.push(nodeRef(item.fragmentId, node.id));
      continue;
    }
    visited.add(visitKey);
    report.reachableNodeRefs.push(nodeRef(item.fragmentId, node.id));
    if (!report.contextIds.includes(item.contextId)) report.contextIds.push(item.contextId);

    let nextContext = item.contextId;
    let nextEncoder = item.encoder;
    let nextIndex = item.nodeIndex + 1;

    if (node.kind === "return") {
      continue;
    }

    if (node.kind === "literal") {
      report.literalValues.push(node.value);
      if (node.contextAfter && node.contextAfter !== item.contextId) {
        nextEncoder = undefined;
      }
      nextContext = node.contextAfter ?? nextContext;
    } else if (node.kind === "boundary") {
      if (node.contextAfter && node.contextAfter !== item.contextId) {
        nextEncoder = undefined;
      }
      nextContext = node.contextAfter ?? nextContext;
    } else if (node.kind === "field") {
      const fieldObservation = observeField(node, item.fragmentId, item.contextId, report);
      report.fields.push(fieldObservation);
      if (!node.authorizedContextIds.includes(item.contextId)) {
        report.findings.push({
          code: "TAIL_FIELD_CONTEXT_UNAUTHORIZED",
          severity: "error",
          message: `Field ${node.bindingId} is not authorized in ${item.contextId}`,
          evidence: { nodeId: node.id, contextId: item.contextId },
        });
      }
      if (node.contextId !== item.contextId) {
        report.findings.push({
          code: "TAIL_LEXICAL_CONTEXT_MISMATCH",
          severity: "error",
          message: `Field ${node.id} expects ${node.contextId}, tail state is ${item.contextId}`,
        });
      }
      const encoderId = node.encoderId;
      const table = encoders?.[encoderId];
      const stateId = node.stateId ?? table?.initialStateId ?? item.encoder?.stateId;
      if (encoders && table && stateId) {
        report.findings.push(...checkEncoderStatePolicy(table, stateId));
        if (table.states[stateId]?.contextId !== item.contextId) {
          report.findings.push({
            code: "TAIL_ENCODER_CONTEXT_MISMATCH",
            severity: "error",
            message: `Encoder state for field ${node.id} does not match lexical context`,
          });
        }
        nextEncoder = {
          encoderId,
          stateId: node.stateAfterId ?? stateId,
        };
      } else if (encoders) {
        report.findings.push({
          code: "TAIL_ENCODER_STATE_UNKNOWN",
          severity: "error",
          message: `Could not resolve encoder state for field ${node.id}`,
        });
      }
      nextContext = node.contextAfter ?? nextContext;
    } else if (node.kind === "call") {
      const target = template.fragments[node.targetFragmentId];
      if (!target) {
        report.findings.push({
          code: "TAIL_CALL_TARGET_MISSING",
          severity: "error",
          message: `Call ${node.id} targets missing fragment ${node.targetFragmentId}`,
        });
      } else {
        queue.push({
          fragmentId: target.id,
          nodeIndex: 0,
          contextId: item.contextId,
          encoder: item.encoder,
        });
      }
      const returnPointIndex = getNodeIndex(template, item.fragmentId, node.returnPointId);
      if (returnPointIndex < 0) {
        report.findings.push({
          code: "TAIL_RETURN_POINT_MISSING",
          severity: "error",
          message: `Call ${node.id} return point ${node.returnPointId} is missing`,
        });
      } else {
        nextIndex = returnPointIndex;
      }
      nextContext = node.contextAfter ?? nextContext;
    }

    if (nextIndex < fragment.nodes.length) {
      queue.push({
        fragmentId: item.fragmentId,
        nodeIndex: nextIndex,
        contextId: nextContext,
        encoder: nextEncoder,
      });
    }
  }

  return report;
}

function observeField(
  node: FieldNode,
  fragmentId: string,
  contextId: ContextId,
  report: TailReport,
): TailFieldObservation {
  if (node.contextId !== contextId) {
    report.findings.push({
      code: "FIELD_DECLARED_CONTEXT_MISMATCH",
      severity: "error",
      message: `Field ${node.id} declares ${node.contextId} but analysis context is ${contextId}`,
    });
  }
  return {
    ref: nodeRef(fragmentId, node.id),
    fragmentId,
    nodeId: node.id,
    bindingId: node.bindingId,
    sourceLabel: node.sourceLabel,
    contextId: node.contextId,
    authorizedContextIds: node.authorizedContextIds,
    encoderId: node.encoderId,
    encoderStateId: node.stateId,
  };
}

export function sourceLabelsCompatible(oldLabel: string, newLabel: string): boolean {
  if (oldLabel === newLabel) return true;
  const oldParts = oldLabel.split(":");
  const newParts = newLabel.split(":");
  if (oldParts[0] !== newParts[0]) return false;
  return newLabel.endsWith(`:stricter-than(${oldLabel})`);
}

export function closeTokensAppearInOrder(literalValues: string[], tokens: string[]): boolean {
  let haystack = literalValues.join("");
  let from = 0;
  for (const token of tokens) {
    const index = haystack.indexOf(token, from);
    if (index < 0) return false;
    from = index + token.length;
  }
  return true;
}

export function templateHashesEqual(a: string, b: string): boolean {
  return a === b;
}
