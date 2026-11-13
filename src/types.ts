export type Namespace = "html" | "svg";

export interface TextNode {
  type: "text";
  data: string;
}

export interface ElementNode {
  type: "element";
  tagName: string;
  namespace: Namespace;
  attributes: Record<string, string>;
  children: Node[];
  selfClosing: boolean;
  // HTML integration point: true for foreignObject and everything inside it.
  inForeign: boolean;
}

export type Node = ElementNode | TextNode;

export interface DocumentNode {
  type: "document";
  children: Node[];
}

export type Token =
  | { kind: "doctype" }
  | { kind: "comment" }
  | { kind: "text"; data: string }
  | {
      kind: "startTag";
      tagName: string;
      attributes: Array<[string, string]>;
      selfClosing: boolean;
    }
  | { kind: "endTag"; tagName: string };

export interface RemovedItem {
  reason: "element" | "attribute" | "url";
  tag: string;
  name: string;
  value?: string;
}

export interface SanitizeReport {
  removed: RemovedItem[];
}
