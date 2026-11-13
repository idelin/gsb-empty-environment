import { SVG_CANONICAL } from "./policy.ts";
import type { ElementNode, Node } from "./types.ts";

const HTML_VOID = new Set([
  "area",
  "base",
  "br",
  "col",
  "embed",
  "hr",
  "img",
  "input",
  "link",
  "meta",
  "param",
  "source",
  "track",
  "wbr",
]);

const SVG_VOID = new Set([
  "circle",
  "ellipse",
  "line",
  "path",
  "polygon",
  "polyline",
  "rect",
  "stop",
  "use",
  "image",
]);

function escapeText(value: string): string {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

function escapeAttribute(value: string): string {
  return escapeText(value).replace(/"/g, "&quot;");
}

function renderAttributes(element: ElementNode): string {
  const parts: string[] = [];
  for (const [name, value] of Object.entries(element.attributes)) {
    const renderedName =
      element.namespace === "svg" && SVG_CANONICAL[name]
        ? SVG_CANONICAL[name]
        : name;
    parts.push(`${renderedName}="${escapeAttribute(value)}"`);
  }
  return parts.length === 0 ? "" : " " + parts.join(" ");
}

function serializeElement(element: ElementNode): string {
  const tag = element.tagName;
  const opening = `<${tag}${renderAttributes(element)}>`;

  if (element.namespace === "svg" && SVG_VOID.has(tag)) {
    return `<${tag}${renderAttributes(element)}/>`;
  }
  if (element.namespace === "html" && HTML_VOID.has(tag)) {
    return opening;
  }
  if (element.selfClosing && element.children.length === 0) {
    // Non-void self-closed elements still serialize with an explicit end tag.
    return `${opening}</${tag}>`;
  }

  const inner = element.children.map(serializeNode).join("");
  return `${opening}${inner}</${tag}>`;
}

export function serializeNode(node: Node): string {
  return node.type === "text" ? escapeText(node.data) : serializeElement(node);
}

export function serializeNodes(nodes: Node[]): string {
  return nodes.map(serializeNode).join("");
}
