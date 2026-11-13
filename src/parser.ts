import { decodeCharacterReferences } from "./entities.ts";
import {
  appendChild,
  createCommentNode,
  createDocument,
  createDocumentFragment,
  createDoctypeNode,
  createElement,
  createTextNode,
  type Attribute,
  type ChildNode,
  type DocumentFragmentNode,
  type DocumentNode,
  type ElementNode,
  type ParentNode,
} from "./nodes.ts";

export const RAW_TEXT_ELEMENTS = [
  "noscript",
  "script",
  "style",
  "textarea",
  "title",
] as const;

export type RawTextElement = (typeof RAW_TEXT_ELEMENTS)[number];

const RAW_TEXT_ELEMENT_SET = new Set<string>(RAW_TEXT_ELEMENTS);
const LEGACY_RAW_TEXT_ELEMENT_SET = new Set([
  "xmp",
  "iframe",
  "noembed",
  "noframes",
]);

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

const HEAD_ELEMENTS = new Set([
  "base",
  "basefont",
  "bgsound",
  "link",
  "meta",
  "title",
  "style",
  "script",
  "noscript",
  "template",
]);

const P_CLOSING_START_TAGS = new Set([
  "address",
  "article",
  "aside",
  "blockquote",
  "details",
  "dialog",
  "dir",
  "div",
  "dl",
  "fieldset",
  "figcaption",
  "figure",
  "footer",
  "form",
  "h1",
  "h2",
  "h3",
  "h4",
  "h5",
  "h6",
  "header",
  "hgroup",
  "hr",
  "main",
  "menu",
  "nav",
  "ol",
  "p",
  "pre",
  "section",
  "table",
  "ul",
]);

const IMPLIED_END_TAGS: Record<string, string[]> = {
  li: ["li"],
  dt: ["dt", "dd"],
  dd: ["dt", "dd"],
  option: ["option"],
  optgroup: ["option", "optgroup"],
  tr: ["tr"],
  td: ["td", "th"],
  th: ["td", "th"],
  thead: ["thead", "tbody", "tfoot"],
  tbody: ["thead", "tbody", "tfoot"],
  tfoot: ["thead", "tbody", "tfoot"],
  colgroup: ["colgroup"],
  caption: ["caption"],
};

export interface ParseOptions {
  scriptingEnabled?: boolean;
  decodeEntities?: boolean;
  context?: string | ElementNode | null;
  fragment?: boolean;
}

interface StartTag {
  tagName: string;
  attrs: Attribute[];
  selfClosing: boolean;
  end: number;
}

interface EndTag {
  tagName: string;
  end: number;
}

function isWhitespace(character: string | undefined): boolean {
  return (
    character === " " ||
    character === "\t" ||
    character === "\n" ||
    character === "\f" ||
    character === "\r"
  );
}

function isAsciiLetter(character: string | undefined): boolean {
  return character !== undefined && /[A-Za-z]/.test(character);
}

function parseStartTag(input: string, start: number): StartTag | null {
  let cursor = start + 1;
  let tagName = "";

  while (
    cursor < input.length &&
    !isWhitespace(input[cursor]) &&
    input[cursor] !== "/" &&
    input[cursor] !== ">"
  ) {
    tagName += input[cursor];
    cursor += 1;
  }

  tagName = tagName.toLowerCase();
  if (tagName === "") {
    return null;
  }

  const attrs: Attribute[] = [];
  const seen = new Set<string>();
  let selfClosing = false;

  while (cursor < input.length) {
    while (isWhitespace(input[cursor])) {
      cursor += 1;
    }

    if (input[cursor] === ">") {
      cursor += 1;
      break;
    }

    if (input[cursor] === "/" && input[cursor + 1] === ">") {
      selfClosing = true;
      cursor += 2;
      break;
    }

    const nameStart = cursor;
    while (
      cursor < input.length &&
      !isWhitespace(input[cursor]) &&
      input[cursor] !== "=" &&
      input[cursor] !== "/" &&
      input[cursor] !== ">"
    ) {
      cursor += 1;
    }

    const name = input.slice(nameStart, cursor).toLowerCase();
    if (name === "") {
      cursor += 1;
      continue;
    }

    while (isWhitespace(input[cursor])) {
      cursor += 1;
    }

    let value = "";
    if (input[cursor] === "=") {
      cursor += 1;
      while (isWhitespace(input[cursor])) {
        cursor += 1;
      }

      const quote = input[cursor];
      if (quote === '"' || quote === "'") {
        cursor += 1;
        const valueStart = cursor;
        while (cursor < input.length && input[cursor] !== quote) {
          cursor += 1;
        }
        value = input.slice(valueStart, cursor);
        if (input[cursor] === quote) {
          cursor += 1;
        }
      } else {
        const valueStart = cursor;
        while (
          cursor < input.length &&
          !isWhitespace(input[cursor]) &&
          input[cursor] !== ">"
        ) {
          cursor += 1;
        }
        value = input.slice(valueStart, cursor);
      }
      value = decodeCharacterReferences(value);
    }

    if (!seen.has(name)) {
      seen.add(name);
      attrs.push({ name, value });
    }
  }

  return {
    tagName,
    attrs,
    selfClosing,
    end: cursor,
  };
}

function parseEndTag(input: string, start: number): EndTag | null {
  let cursor = start + 2;
  while (isWhitespace(input[cursor])) {
    cursor += 1;
  }

  const nameStart = cursor;
  while (
    cursor < input.length &&
    !isWhitespace(input[cursor]) &&
    input[cursor] !== "/" &&
    input[cursor] !== ">"
  ) {
    cursor += 1;
  }

  const tagName = input.slice(nameStart, cursor).toLowerCase();
  if (tagName === "") {
    return null;
  }

  while (cursor < input.length && input[cursor] !== ">") {
    cursor += 1;
  }
  if (input[cursor] === ">") {
    cursor += 1;
  }

  return { tagName, end: cursor };
}

function findRawtextEnd(
  input: string,
  start: number,
  tagName: string,
): number {
  const lowerInput = input.toLowerCase();
  const needle = `</${tagName}`;
  let cursor = start;

  while (cursor < input.length) {
    const found = lowerInput.indexOf(needle, cursor);
    if (found === -1) {
      return -1;
    }

    const next = input[found + needle.length];
    if (
      next === undefined ||
      next === ">" ||
      next === "/" ||
      isWhitespace(next)
    ) {
      return found;
    }

    cursor = found + needle.length;
  }

  return -1;
}

function isRawtextElement(
  tagName: string,
  options: ParseOptions,
): boolean {
  if (tagName === "plaintext") {
    return true;
  }
  if (RAW_TEXT_ELEMENT_SET.has(tagName)) {
    if (tagName === "noscript") {
      return options.scriptingEnabled !== false;
    }
    return true;
  }
  return LEGACY_RAW_TEXT_ELEMENT_SET.has(tagName);
}

function stackIndexOf(stack: ParentNode[], tagName: string): number {
  for (let index = stack.length - 1; index > 0; index -= 1) {
    const node = stack[index];
    if (node.type === "element" && node.tagName === tagName) {
      return index;
    }
  }
  return -1;
}

function popThrough(stack: ParentNode[], index: number): void {
  if (index > 0) {
    stack.length = index;
  }
}

function applyImpliedEndTags(
  stack: ParentNode[],
  tagName: string,
): void {
  const current = stack[stack.length - 1];
  if (current?.type !== "element") {
    return;
  }

  if (
    tagName === "p" ||
    P_CLOSING_START_TAGS.has(tagName)
  ) {
    const paragraphIndex = stackIndexOf(stack, "p");
    if (paragraphIndex !== -1) {
      popThrough(stack, paragraphIndex);
    }
  }

  if (
    (tagName[0] === "h" && tagName.length === 2 && /[1-6]/.test(tagName[1])) ||
    tagName === "h1"
  ) {
    for (const heading of ["h1", "h2", "h3", "h4", "h5", "h6"]) {
      const headingIndex = stackIndexOf(stack, heading);
      if (headingIndex !== -1) {
        popThrough(stack, headingIndex);
        break;
      }
    }
  }

  const implied = IMPLIED_END_TAGS[tagName];
  if (implied === undefined) {
    return;
  }

  for (const candidate of implied) {
    const candidateIndex = stackIndexOf(stack, candidate);
    if (candidateIndex !== -1) {
      popThrough(stack, candidateIndex);
      return;
    }
  }
}

function appendToCurrent(
  stack: ParentNode[],
  node: ChildNode,
): void {
  appendChild(stack[stack.length - 1], node);
}

function parseNodesInto(
  root: ParentNode,
  input: string,
  options: ParseOptions,
): void {
  const stack: ParentNode[] = [root];
  const decodeEntities = options.decodeEntities !== false;
  let pendingText = "";
  let cursor = 0;

  const flushText = (): void => {
    if (pendingText === "") {
      return;
    }
    const value = decodeEntities
      ? decodeCharacterReferences(pendingText)
      : pendingText;
    appendToCurrent(stack, createTextNode(value));
    pendingText = "";
  };

  while (cursor < input.length) {
    if (input[cursor] !== "<") {
      const nextTag = input.indexOf("<", cursor);
      const end = nextTag === -1 ? input.length : nextTag;
      pendingText += input.slice(cursor, end);
      cursor = end;
      continue;
    }

    if (input.startsWith("<!--", cursor)) {
      flushText();
      const commentEnd = input.indexOf("-->", cursor + 4);
      if (commentEnd === -1) {
        appendToCurrent(
          stack,
          createCommentNode(input.slice(cursor + 4)),
        );
        cursor = input.length;
      } else {
        appendToCurrent(
          stack,
          createCommentNode(input.slice(cursor + 4, commentEnd)),
        );
        cursor = commentEnd + 3;
      }
      continue;
    }

    if (/^<!doctype/i.test(input.slice(cursor, cursor + 9))) {
      flushText();
      const doctypeEnd = input.indexOf(">", cursor + 2);
      const body =
        doctypeEnd === -1
          ? input.slice(cursor + 2)
          : input.slice(cursor + 2, doctypeEnd);
      const parts = body.trim().split(/\s+/);
      const name = (parts[1] ?? "html").toLowerCase();
      appendToCurrent(stack, createDoctypeNode(name));
      cursor = doctypeEnd === -1 ? input.length : doctypeEnd + 1;
      continue;
    }

    if (input.startsWith("<![CDATA[", cursor)) {
      flushText();
      const cdataEnd = input.indexOf("]]>", cursor + 9);
      if (cdataEnd === -1) {
        pendingText += input.slice(cursor + 9);
        cursor = input.length;
      } else {
        pendingText += input.slice(cursor + 9, cdataEnd);
        cursor = cdataEnd + 3;
      }
      continue;
    }

    if (input.startsWith("</", cursor)) {
      flushText();
      const endTag = parseEndTag(input, cursor);
      if (endTag === null) {
        pendingText += "<";
        cursor += 1;
        continue;
      }

      const openIndex = stackIndexOf(stack, endTag.tagName);
      if (openIndex !== -1) {
        popThrough(stack, openIndex);
      }
      cursor = endTag.end;
      continue;
    }

    if (isAsciiLetter(input[cursor + 1])) {
      flushText();
      const startTag = parseStartTag(input, cursor);
      if (startTag === null) {
        pendingText += "<";
        cursor += 1;
        continue;
      }

      applyImpliedEndTags(stack, startTag.tagName);
      const element = createElement(startTag.tagName, startTag.attrs);
      appendToCurrent(stack, element);
      cursor = startTag.end;

      if (VOID_ELEMENTS.has(startTag.tagName)) {
        continue;
      }

      if (isRawtextElement(startTag.tagName, options)) {
        if (startTag.tagName === "plaintext") {
          const value = input.slice(cursor);
          if (value !== "") {
            appendChild(element, createTextNode(value));
          }
          cursor = input.length;
          continue;
        }

        const rawtextEnd = findRawtextEnd(input, cursor, startTag.tagName);
        const contentEnd = rawtextEnd === -1 ? input.length : rawtextEnd;
        if (contentEnd > cursor) {
          appendChild(
            element,
            createTextNode(input.slice(cursor, contentEnd)),
          );
        }

        if (rawtextEnd === -1) {
          cursor = input.length;
        } else {
          const endTag = parseEndTag(input, rawtextEnd);
          cursor = endTag === null ? rawtextEnd + 2 : endTag.end;
        }
        continue;
      }

      stack.push(element);
      continue;
    }

    pendingText += "<";
    cursor += 1;
  }

  flushText();
}

export function parseFragment(
  input: string,
  options: ParseOptions = {},
): DocumentFragmentNode {
  const fragment = createDocumentFragment();
  parseNodesInto(fragment, input, options);
  return fragment;
}

function splitDocumentChildren(
  document: DocumentNode,
  roots: ChildNode[],
): void {
  const htmlElement = roots.find(
    (node): node is ElementNode =>
      node.type === "element" && node.tagName === "html",
  );

  const html = htmlElement ?? createElement("html");
  document.childNodes.push(html);

  const existingHead =
    htmlElement?.childNodes.find(
      (node): node is ElementNode =>
        node.type === "element" && node.tagName === "head",
    ) ?? createElement("head");
  const existingBody =
    htmlElement?.childNodes.find(
      (node): node is ElementNode =>
        node.type === "element" && node.tagName === "body",
    ) ?? createElement("body");

  const looseChildren: ChildNode[] = [];
  if (htmlElement === undefined) {
    looseChildren.push(...roots);
  } else {
    for (const node of roots) {
      if (node === htmlElement) {
        continue;
      }
      looseChildren.push(node);
    }
    for (const node of htmlElement.childNodes) {
      if (node !== existingHead && node !== existingBody) {
        looseChildren.push(node);
      }
    }
  }

  const head = existingHead;
  const body = existingBody;
  html.childNodes = [head, body];

  let inBody = false;
  for (const node of looseChildren) {
    if (node.type === "element" && node.tagName === "body") {
      inBody = true;
      for (const child of node.childNodes) {
        body.childNodes.push(child);
      }
      continue;
    }
    if (node.type === "element" && node.tagName === "head") {
      for (const child of node.childNodes) {
        head.childNodes.push(child);
      }
      continue;
    }

    if (
      !inBody &&
      (node.type === "doctype" ||
        (node.type === "element" && HEAD_ELEMENTS.has(node.tagName)))
    ) {
      if (node.type === "doctype") {
        document.childNodes.unshift(node);
      } else {
        head.childNodes.push(node);
      }
      continue;
    }

    if (!inBody && node.type === "text" && node.value.trim() === "") {
      head.childNodes.push(node);
      continue;
    }

    inBody = true;
    body.childNodes.push(node);
  }
}

export function parseDocument(
  input: string,
  options: ParseOptions = {},
): DocumentNode {
  const fragment = parseFragment(input, options);
  const document = createDocument();
  splitDocumentChildren(document, fragment.childNodes);
  return document;
}

export function parse(
  input: string,
  options: ParseOptions = {},
): DocumentNode | DocumentFragmentNode {
  return options.fragment === true
    ? parseFragment(input, options)
    : parseDocument(input, options);
}

export const parseHTML = parse;
export const parseHtml = parse;
export const parseHTMLFragment = parseFragment;
export const parseHTMLDocument = parseDocument;
