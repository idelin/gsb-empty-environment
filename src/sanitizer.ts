import {
  isAttributeAllowed,
  isElementAllowed,
  isEventAttribute,
  isSafeUrl,
  URL_ATTRIBUTES,
} from "./policy.ts";
import { serializeNodes } from "./serializer.ts";
import { buildDocument } from "./tree-builder.ts";
import type {
  ElementNode,
  Node,
  RemovedItem,
  SanitizeReport,
} from "./types.ts";

export interface SanitizeOptions {
  report?: SanitizeReport;
}

// Raw-text elements whose textual content is executable and must never
// leak out when the wrapper element itself is removed.
const DANGLING_RAW_TEXT = new Set(["script", "style", "iframe", "noscript"]);

function sanitizeNodes(
  nodes: Node[],
  report: SanitizeReport,
): Node[] {
  const output: Node[] = [];

  for (const node of nodes) {
    if (node.type === "text") {
      output.push(node);
      continue;
    }

    const element = node as ElementNode;
    if (
      !isElementAllowed(
        element.tagName,
        element.namespace,
        element.inForeign,
      )
    ) {
      report.removed.push({
        reason: "element",
        tag: element.tagName,
        name: element.tagName,
      });
      if (DANGLING_RAW_TEXT.has(element.tagName)) {
        continue;
      }
      // Drop the disallowed wrapper but keep any safe descendants.
      output.push(...sanitizeNodes(element.children, report));
      continue;
    }

    const allowedAttributes: Record<string, string> = {};
    for (const [name, value] of Object.entries(element.attributes)) {
      if (isEventAttribute(name)) {
        report.removed.push({
          reason: "attribute",
          tag: element.tagName,
          name,
          value,
        });
        continue;
      }

      if (
        !isAttributeAllowed(
          element.tagName,
          name,
          element.namespace,
          element.inForeign,
        )
      ) {
        report.removed.push({
          reason: "attribute",
          tag: element.tagName,
          name,
          value,
        });
        continue;
      }

      if (URL_ATTRIBUTES.has(name) && !isSafeUrl(value)) {
        report.removed.push({
          reason: "url",
          tag: element.tagName,
          name,
          value,
        });
        continue;
      }

      allowedAttributes[name] = value;
    }

    output.push({
      type: "element",
      tagName: element.tagName,
      namespace: element.namespace,
      attributes: allowedAttributes,
      children: sanitizeNodes(element.children, report),
      selfClosing: element.selfClosing,
      inForeign: element.inForeign,
    });
  }

  return output;
}

export function sanitizeDocument(
  html: string,
  options: SanitizeOptions = {},
): string {
  const report = options.report ?? { removed: [] };
  const documentNode = buildDocument(html);
  const clean = sanitizeNodes(documentNode.children, report);
  return serializeNodes(clean);
}

export function sanitizeTree(
  html: string,
  report: SanitizeReport = { removed: [] },
): { html: string; report: SanitizeReport; removed: RemovedItem[] } {
  const cleanHtml = sanitizeDocument(html, { report });
  return { html: cleanHtml, report, removed: report.removed };
}
