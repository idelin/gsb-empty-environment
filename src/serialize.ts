import { escapeAttribute, escapeText } from "./entities.ts";
import type { ChildNode, Node } from "./nodes.ts";
import { RAW_TEXT_ELEMENTS } from "./parser.ts";

const VOID_ELEMENTS = new Set([
  "area",
  "base",
  "basefont",
  "bgsound",
  "br",
  "col",
  "embed",
  "frame",
  "hr",
  "img",
  "input",
  "keygen",
  "link",
  "meta",
  "param",
  "source",
  "track",
  "wbr",
]);

const RAW_TEXT_ELEMENT_SET = new Set<string>([
  ...RAW_TEXT_ELEMENTS,
  "xmp",
  "iframe",
  "noembed",
  "noframes",
  "plaintext",
]);

function serializeAttributes(
  attrs: Array<{ name: string; value: string }>,
): string {
  let output = "";
  for (const attr of attrs) {
    output += ` ${attr.name}="${escapeAttribute(attr.value)}"`;
  }
  return output;
}

function serializeNode(node: Node, rawTextParent: boolean): string {
  switch (node.type) {
    case "document":
    case "document-fragment":
      return node.childNodes
        .map((child) => serializeNode(child, false))
        .join("");
    case "element": {
      const open = `<${node.tagName}${serializeAttributes(node.attrs)}>`;
      if (VOID_ELEMENTS.has(node.tagName)) {
        return open;
      }
      const rawChildren = RAW_TEXT_ELEMENT_SET.has(node.tagName);
      const content = node.childNodes
        .map((child) => serializeNode(child, rawChildren))
        .join("");
      return `${open}${content}</${node.tagName}>`;
    }
    case "text":
      return rawTextParent ? node.value : escapeText(node.value);
    case "comment":
      return `<!--${node.value}-->`;
    case "doctype":
      return `<!DOCTYPE ${node.name}>`;
  }
}

export function serialize(node: Node | ChildNode[]): string {
  if (Array.isArray(node)) {
    return node.map((child) => serializeNode(child, false)).join("");
  }
  return serializeNode(node, false);
}

export const serializeFragment = serialize;
export const toHTML = serialize;
