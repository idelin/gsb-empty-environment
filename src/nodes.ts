export const HTML_NAMESPACE = "http://www.w3.org/1999/xhtml";

export interface Attribute {
  name: string;
  value: string;
}

export interface TextNode {
  type: "text";
  nodeName: "#text";
  readonly nodeType: 3;
  readonly textContent: string;
  value: string;
  data: string;
  childNodes: [];
  children: [];
}

export interface CommentNode {
  type: "comment";
  nodeName: "#comment";
  readonly nodeType: 8;
  readonly textContent: string;
  value: string;
  data: string;
  childNodes: [];
  children: [];
}

export interface DoctypeNode {
  type: "doctype";
  nodeName: "#documentType";
  readonly nodeType: 10;
  readonly textContent: "";
  name: string;
  publicId: string;
  systemId: string;
  childNodes: [];
  children: [];
}

export interface ElementNode {
  type: "element";
  nodeName: string;
  readonly nodeType: 1;
  tagName: string;
  namespaceURI: string;
  attrs: Attribute[];
  attributes: Record<string, string>;
  childNodes: ChildNode[];
  children: ChildNode[];
  readonly textContent: string;
  readonly elementChildren: ElementNode[];
  getElementsByTagName(tagName: string): ElementNode[];
  querySelectorAll(selector: string): ElementNode[];
  querySelector(selector: string): ElementNode | null;
  getElementById(id: string): ElementNode | null;
}

export interface DocumentFragmentNode {
  type: "document-fragment";
  nodeName: "#document-fragment";
  readonly nodeType: 11;
  childNodes: ChildNode[];
  children: ChildNode[];
  readonly textContent: string;
  readonly elementChildren: ElementNode[];
  getElementsByTagName(tagName: string): ElementNode[];
  querySelectorAll(selector: string): ElementNode[];
  querySelector(selector: string): ElementNode | null;
  getElementById(id: string): ElementNode | null;
}

export interface DocumentNode {
  type: "document";
  nodeName: "#document";
  readonly nodeType: 9;
  childNodes: ChildNode[];
  children: ChildNode[];
  readonly textContent: string;
  readonly documentElement: ElementNode | null;
  readonly head: ElementNode | null;
  readonly body: ElementNode | null;
  getElementsByTagName(tagName: string): ElementNode[];
  querySelectorAll(selector: string): ElementNode[];
  querySelector(selector: string): ElementNode | null;
  getElementById(id: string): ElementNode | null;
}

export type ParentNode =
  | ElementNode
  | DocumentFragmentNode
  | DocumentNode;

export type ChildNode = ElementNode | TextNode | CommentNode | DoctypeNode;

export type Node = ParentNode | ChildNode;

export function isElement(node: Node): node is ElementNode {
  return node.type === "element";
}

export function isText(node: Node): node is TextNode {
  return node.type === "text";
}

export function isParentNode(node: Node): node is ParentNode {
  return (
    node.type === "element" ||
    node.type === "document" ||
    node.type === "document-fragment"
  );
}

export function getTextContent(node: Node): string {
  if (node.type === "text") {
    return node.value;
  }
  if (isParentNode(node)) {
    let output = "";
    for (const child of node.childNodes) {
      output += getTextContent(child);
    }
    return output;
  }
  return "";
}

function collectDescendantElements(
  node: ParentNode,
  output: ElementNode[],
): void {
  for (const child of node.childNodes) {
    if (child.type === "element") {
      output.push(child);
      collectDescendantElements(child, output);
    }
  }
}

export function getElementsByTagName(
  node: Node,
  tagName: string,
): ElementNode[] {
  if (!isParentNode(node)) {
    return [];
  }

  const normalized = tagName.toLowerCase();
  const output: ElementNode[] = [];
  collectDescendantElements(node, output);

  return normalized === "*"
    ? output
    : output.filter((element) => element.tagName === normalized);
}

interface SimpleSelector {
  tagName: string | null;
  id: string | null;
  classes: string[];
  attributes: Array<{ name: string; value: string | null }>;
}

function parseSimpleSelector(selector: string): SimpleSelector | null {
  const trimmed = selector.trim();
  if (trimmed === "") {
    return null;
  }

  const result: SimpleSelector = {
    tagName: null,
    id: null,
    classes: [],
    attributes: [],
  };
  let cursor = 0;

  const tagMatch = /^(\*|[A-Za-z][A-Za-z0-9:_-]*)/.exec(trimmed);
  if (tagMatch !== null) {
    result.tagName = tagMatch[1] === "*" ? null : tagMatch[1].toLowerCase();
    cursor = tagMatch[0].length;
  }

  while (cursor < trimmed.length) {
    const character = trimmed[cursor];
    if (character === "#") {
      const match = /^#([A-Za-z0-9:_-]+)/.exec(trimmed.slice(cursor));
      if (match === null) {
        return null;
      }
      result.id = match[1];
      cursor += match[0].length;
      continue;
    }
    if (character === ".") {
      const match = /^\.([A-Za-z0-9:_-]+)/.exec(trimmed.slice(cursor));
      if (match === null) {
        return null;
      }
      result.classes.push(match[1]);
      cursor += match[0].length;
      continue;
    }
    if (character === "[") {
      const match = /^\[\s*([A-Za-z0-9:_-]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\]]*)))?\s*\]/.exec(
        trimmed.slice(cursor),
      );
      if (match === null) {
        return null;
      }
      result.attributes.push({
        name: match[1].toLowerCase(),
        value: match[2] ?? match[3] ?? match[4]?.trim() ?? null,
      });
      cursor += match[0].length;
      continue;
    }
    return null;
  }

  return result;
}

function matchesSimpleSelector(
  element: ElementNode,
  selector: SimpleSelector,
): boolean {
  if (
    selector.tagName !== null &&
    element.tagName !== selector.tagName
  ) {
    return false;
  }
  if (
    selector.id !== null &&
    element.attributes.id !== selector.id
  ) {
    return false;
  }
  if (selector.classes.length > 0) {
    const classes = new Set(
      (element.attributes.class ?? "").split(/\s+/).filter(Boolean),
    );
    if (!selector.classes.every((className) => classes.has(className))) {
      return false;
    }
  }
  for (const attribute of selector.attributes) {
    if (!(attribute.name in element.attributes)) {
      return false;
    }
    if (
      attribute.value !== null &&
      element.attributes[attribute.name] !== attribute.value
    ) {
      return false;
    }
  }
  return true;
}

export function querySelectorAll(
  node: Node,
  selector: string,
): ElementNode[] {
  if (!isParentNode(node)) {
    return [];
  }

  const selectors = selector
    .split(",")
    .map(parseSimpleSelector)
    .filter((item): item is SimpleSelector => item !== null);
  if (selectors.length === 0) {
    return [];
  }

  const elements: ElementNode[] = [];
  collectDescendantElements(node, elements);
  return elements.filter((element) =>
    selectors.some((item) => matchesSimpleSelector(element, item)),
  );
}

export function querySelector(
  node: Node,
  selector: string,
): ElementNode | null {
  return querySelectorAll(node, selector)[0] ?? null;
}

export function getElementById(
  node: Node,
  id: string,
): ElementNode | null {
  return querySelector(node, `#${id}`);
}

function defineNodeView(node: Node): void {
  if (node.type === "text" || node.type === "comment") {
    Object.defineProperty(node, "textContent", {
      get() {
        return this.value;
      },
      enumerable: false,
    });
    return;
  }

  if (node.type === "doctype") {
    Object.defineProperty(node, "textContent", {
      value: "",
      enumerable: false,
    });
    return;
  }

  Object.defineProperties(node, {
    textContent: {
      get() {
        return getTextContent(this);
      },
      enumerable: false,
    },
    elementChildren: {
      get() {
        return this.childNodes.filter(isElement);
      },
      enumerable: false,
    },
    getElementsByTagName: {
      value(tagName: string) {
        return getElementsByTagName(this, tagName);
      },
      enumerable: false,
    },
    querySelectorAll: {
      value(selector: string) {
        return querySelectorAll(this, selector);
      },
      enumerable: false,
    },
    querySelector: {
      value(selector: string) {
        return querySelector(this, selector);
      },
      enumerable: false,
    },
    getElementById: {
      value(id: string) {
        return getElementById(this, id);
      },
      enumerable: false,
    },
  });
}

export function createTextNode(value: string): TextNode {
  const node: TextNode = {
    type: "text",
    nodeName: "#text",
    nodeType: 3,
    textContent: value,
    value,
    data: value,
    childNodes: [],
    children: [],
  };
  defineNodeView(node);
  return node;
}

export function createCommentNode(value: string): CommentNode {
  const node: CommentNode = {
    type: "comment",
    nodeName: "#comment",
    nodeType: 8,
    textContent: value,
    value,
    data: value,
    childNodes: [],
    children: [],
  };
  defineNodeView(node);
  return node;
}

export function createDoctypeNode(
  name = "html",
  publicId = "",
  systemId = "",
): DoctypeNode {
  const node: DoctypeNode = {
    type: "doctype",
    nodeName: "#documentType",
    nodeType: 10,
    textContent: "",
    name,
    publicId,
    systemId,
    childNodes: [],
    children: [],
  };
  defineNodeView(node);
  return node;
}

export function createElement(
  tagName: string,
  attrs: Attribute[] = [],
): ElementNode {
  const childNodes: ChildNode[] = [];
  const normalizedAttrs = attrs.map((attr) => ({
    name: attr.name.toLowerCase(),
    value: attr.value,
  }));
  const attributes: Record<string, string> = {};
  for (const attr of normalizedAttrs) {
    if (!Object.prototype.hasOwnProperty.call(attributes, attr.name)) {
      attributes[attr.name] = attr.value;
    }
  }

  const node: ElementNode = {
    type: "element",
    nodeName: tagName,
    nodeType: 1,
    tagName,
    namespaceURI: HTML_NAMESPACE,
    attrs: normalizedAttrs,
    attributes,
    childNodes,
    children: childNodes,
    textContent: "",
    elementChildren: [],
    getElementsByTagName: () => [],
    querySelectorAll: () => [],
    querySelector: () => null,
    getElementById: () => null,
  };
  defineNodeView(node);
  return node;
}

export function createDocumentFragment(): DocumentFragmentNode {
  const childNodes: ChildNode[] = [];
  const node: DocumentFragmentNode = {
    type: "document-fragment",
    nodeName: "#document-fragment",
    nodeType: 11,
    childNodes,
    children: childNodes,
    textContent: "",
    elementChildren: [],
    getElementsByTagName: () => [],
    querySelectorAll: () => [],
    querySelector: () => null,
    getElementById: () => null,
  };
  defineNodeView(node);
  return node;
}

export function createDocument(): DocumentNode {
  const childNodes: ChildNode[] = [];
  const node: DocumentNode = {
    type: "document",
    nodeName: "#document",
    nodeType: 9,
    childNodes,
    children: childNodes,
    textContent: "",
    documentElement: null,
    head: null,
    body: null,
    getElementsByTagName: () => [],
    querySelectorAll: () => [],
    querySelector: () => null,
    getElementById: () => null,
  };
  defineNodeView(node);
  Object.defineProperties(node, {
    documentElement: {
      get() {
        return (
          this.childNodes.find(
            (child): child is ElementNode =>
              isElement(child) && child.tagName === "html",
          ) ?? null
        );
      },
      enumerable: false,
    },
    head: {
      get() {
        return this.documentElement?.elementChildren.find(
          (child) => child.tagName === "head",
        ) ?? null;
      },
      enumerable: false,
    },
    body: {
      get() {
        return this.documentElement?.elementChildren.find(
          (child) => child.tagName === "body",
        ) ?? null;
      },
      enumerable: false,
    },
  });
  return node;
}

export function appendChild(parent: ParentNode, child: ChildNode): void {
  parent.childNodes.push(child);
}

export function getAttribute(
  element: ElementNode,
  name: string,
): string | null {
  const lowerName = name.toLowerCase();
  for (const attr of element.attrs) {
    if (attr.name === lowerName) {
      return attr.value;
    }
  }
  return null;
}
