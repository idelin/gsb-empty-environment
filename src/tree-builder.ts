import { tokenize } from "./tokenizer.ts";
import type { DocumentNode, ElementNode, Namespace, Node } from "./types.ts";

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

type OpenElement = ElementNode;

export function buildDocument(html: string): DocumentNode {
  const documentNode: DocumentNode = { type: "document", children: [] };
  const stack: OpenElement[] = [];

  const currentParent = (): { children: Node[] } =>
    stack.length === 0 ? documentNode : stack[stack.length - 1];

      const currentNamespace = (): Namespace =>
    stack.length === 0 ? "html" : stack[stack.length - 1].namespace;

  const inForeign = (): boolean =>
    stack.length !== 0 && stack[stack.length - 1].inForeign;

  for (const token of tokenize(html)) {
    if (token.kind === "text") {
      currentParent().children.push({ type: "text", data: token.data });
    } else if (token.kind === "startTag") {
      const namespace: Namespace =
        token.tagName === "svg" && currentNamespace() === "html"
          ? "svg"
          : currentNamespace();
      const withinForeign =
        inForeign() ||
        (token.tagName === "foreignobject" && namespace === "svg");
      const element: OpenElement = {
        type: "element",
        tagName: token.tagName,
        namespace,
        attributes: Object.fromEntries(token.attributes),
        children: [],
        selfClosing: token.selfClosing,
        inForeign: withinForeign,
      };
      currentParent().children.push(element);

      const isVoid =
        namespace === "svg"
          ? SVG_VOID.has(token.tagName)
          : HTML_VOID.has(token.tagName);
      if (!token.selfClosing && !isVoid) {
        stack.push(element);
      }
    } else if (token.kind === "endTag") {
      for (let index = stack.length - 1; index >= 0; index--) {
        if (stack[index].tagName === token.tagName) {
          stack.length = index;
          break;
        }
      }
    }
    // comments and doctypes are intentionally dropped from the tree
  }

  return documentNode;
}
