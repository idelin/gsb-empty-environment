/**
 * Minimal, entity-safe XML reader for UPnP device descriptors.
 *
 * UPnP descriptions are small, server-generated XML; a full external parser
 * is unavailable in this environment and a hand-written reader makes the
 * security posture explicit:
 *
 *  - DOCTYPE declarations are rejected outright, so neither internal DTDs
 *    nor external entity references (XXE) can ever be processed.
 *  - Processing instructions and comments are skipped.
 *  - The only entity references accepted are the five predefined XML ones.
 *    There is no entity table, so parameter entities and custom entity
 *    expansion (including the billion-laughs pattern) cannot occur.
 *  - CDATA is consumed as raw text and never re-parsed.
 */

import { MalformedXmlError, UnsafeXmlError } from "./errors.ts";

export interface XmlElement {
  /** Local tag name: namespace prefix is stripped when present. */
  tag: string;
  attributes: Record<string, string>;
  children: XmlElement[];
  /** Concatenated text and CDATA content. */
  text: string;
}

const PREDEFINED_ENTITIES: Record<string, string> = {
  amp: "&",
  lt: "<",
  gt: ">",
  quot: '"',
  apos: "'",
};

const WHITESPACE = new Set([" ", "\t", "\r", "\n"]);

class Scanner {
  private position = 0;
  private readonly source: string;

  constructor(source: string) {
    this.source = source;
  }

  get atEnd(): boolean {
    return this.position >= this.source.length;
  }

  peek(offset = 0): string {
    return this.source[this.position + offset] ?? "";
  }

  startsWith(value: string): boolean {
    return this.source.startsWith(value, this.position);
  }

  consume(value: string): void {
    if (!this.startsWith(value)) {
      throw new MalformedXmlError(
        `Expected ${JSON.stringify(value)} at offset ${this.position}`,
      );
    }
    this.position += value.length;
  }

  advance(): string {
    return this.source[this.position++];
  }

  skipWhitespace(): void {
    while (!this.atEnd && WHITESPACE.has(this.peek())) {
      this.position += 1;
    }
  }

  indexOf(value: string): number {
    return this.source.indexOf(value, this.position);
  }

  moveTo(index: number): void {
    this.position = index;
  }

  get offset(): number {
    return this.position;
  }
}

function localName(qualifiedName: string): string {
  const colon = qualifiedName.indexOf(":");
  return colon === -1 ? qualifiedName : qualifiedName.slice(colon + 1);
}

function readName(scanner: Scanner): string {
  const start = scanner.offset;
  while (!scanner.atEnd) {
    const char = scanner.peek();
    if (
      WHITESPACE.has(char) ||
      char === ">" ||
      char === "/" ||
      char === "="
    ) {
      break;
    }
    scanner.advance();
  }
  const name = scanner.source.slice(start, scanner.offset);
  if (name.length === 0) {
    throw new MalformedXmlError(`Missing name at offset ${start}`);
  }
  return name;
}

function decodeReference(raw: string, scanner: Scanner): string {
  if (raw.startsWith("&#")) {
    const isHex = raw[2] === "x" || raw[2] === "X";
    const digits = isHex ? raw.slice(3, -1) : raw.slice(2, -1);
    const radix = isHex ? 16 : 10;
    if (digits.length === 0 || !/^[0-9a-fA-F]+$/.test(digits)) {
      throw new MalformedXmlError(
        `Invalid numeric character reference at offset ${scanner.offset}`,
      );
    }
    const codePoint = parseInt(digits, radix);
    try {
      return String.fromCodePoint(codePoint);
    } catch {
      throw new MalformedXmlError(
        `Character reference out of range at offset ${scanner.offset}`,
      );
    }
  }

  const entityName = raw.slice(1, -1);
  const resolved = PREDEFINED_ENTITIES[entityName];
  if (resolved === undefined) {
    throw new UnsafeXmlError(
      `Custom entity reference &${entityName}; is not allowed; only the ` +
        "five predefined XML entities are supported",
    );
  }
  return resolved;
}

function readText(scanner: Scanner, terminator: "<"): string {
  let result = "";
  while (!scanner.atEnd) {
    const char = scanner.peek();
    if (char === terminator) {
      return result;
    }
    if (char === "&") {
      const end = scanner.indexOf(";");
      if (end === -1) {
        throw new MalformedXmlError(
          `Unterminated entity reference at offset ${scanner.offset}`,
        );
      }
      const raw = scanner.source.slice(scanner.offset, end + 1);
      result += decodeReference(raw, scanner);
      scanner.moveTo(end + 1);
      continue;
    }
    result += char;
    scanner.advance();
  }
  throw new MalformedXmlError("Unterminated text content");
}

function readAttributeValue(scanner: Scanner): string {
  const quote = scanner.peek();
  if (quote !== '"' && quote !== "'") {
    throw new MalformedXmlError(
      `Quoted attribute value expected at offset ${scanner.offset}`,
    );
  }
  scanner.advance();
  let value = "";
  while (!scanner.atEnd && scanner.peek() !== quote) {
    const char = scanner.peek();
    if (char === "<") {
      throw new MalformedXmlError(
        `Illegal '<' in attribute value at offset ${scanner.offset}`,
      );
    }
    if (char === "&") {
      const end = scanner.indexOf(";");
      if (end === -1) {
        throw new MalformedXmlError(
          `Unterminated entity reference at offset ${scanner.offset}`,
        );
      }
      value += decodeReference(
        scanner.source.slice(scanner.offset, end + 1),
        scanner,
      );
      scanner.moveTo(end + 1);
      continue;
    }
    value += char;
    scanner.advance();
  }
  if (scanner.atEnd) {
    throw new MalformedXmlError("Unterminated attribute value");
  }
  scanner.advance();
  return value;
}

function readAttributes(scanner: Scanner): Record<string, string> {
  const attributes: Record<string, string> = {};
  for (;;) {
    scanner.skipWhitespace();
    if (scanner.peek() === ">" || scanner.startsWith("/>")) {
      return attributes;
    }
    const qualifiedName = readName(scanner);
    scanner.skipWhitespace();
    scanner.consume("=");
    scanner.skipWhitespace();
    const value = readAttributeValue(scanner);
    attributes[localName(qualifiedName)] = value;
  }
}

function skipMisc(scanner: Scanner): void {
  if (scanner.startsWith("<!--")) {
    const end = scanner.indexOf("-->");
    if (end === -1) {
      throw new MalformedXmlError("Unterminated comment");
    }
    scanner.moveTo(end + 3);
    return;
  }
  if (scanner.startsWith("<?")) {
    const end = scanner.indexOf("?>");
    if (end === -1) {
      throw new MalformedXmlError("Unterminated processing instruction");
    }
    scanner.moveTo(end + 2);
    return;
  }
  throw new MalformedXmlError(
    `Unrecognized markup at offset ${scanner.offset}`,
  );
}

function readElement(scanner: Scanner): XmlElement {
  scanner.consume("<");
  const qualifiedName = readName(scanner);
  const element: XmlElement = {
    tag: localName(qualifiedName),
    attributes: readAttributes(scanner),
    children: [],
    text: "",
  };

  if (scanner.startsWith("/>")) {
    scanner.consume("/>");
    return element;
  }
  scanner.consume(">");

  for (;;) {
    if (scanner.atEnd) {
      throw new MalformedXmlError(`Unclosed element <${element.tag}>`);
    }
    if (scanner.startsWith("</")) {
      scanner.consume("</");
      const closingName = localName(readName(scanner));
      scanner.skipWhitespace();
      scanner.consume(">");
      if (closingName !== element.tag) {
        throw new MalformedXmlError(
          `Mismatched closing tag </${closingName}> for <${element.tag}>`,
        );
      }
      return element;
    }
    if (scanner.startsWith("<![CDATA[")) {
      scanner.consume("<![CDATA[");
      const end = scanner.indexOf("]]>");
      if (end === -1) {
        throw new MalformedXmlError("Unterminated CDATA section");
      }
      element.text += scanner.source.slice(scanner.offset, end);
      scanner.moveTo(end + 3);
      continue;
    }
    if (scanner.peek() === "<") {
      if (scanner.startsWith("<!--") || scanner.startsWith("<?")) {
        skipMisc(scanner);
        continue;
      }
      element.children.push(readElement(scanner));
      continue;
    }
    element.text += readText(scanner, "<");
  }
}

/**
 * Parses a complete XML document with DOCTYPE/entity hardening applied.
 * The input must already have been size-capped by the caller.
 */
export function parseXml(source: string): XmlElement {
  const scanner = new Scanner(source);

  // Optional UTF-8 BOM and leading whitespace.
  if (scanner.startsWith("\uFEFF")) {
    scanner.consume("\uFEFF");
  }
  scanner.skipWhitespace();

  // XML declaration and any comments / PIs before the root element.
  while (scanner.peek() === "<" && !scanner.startsWith("</")) {
    if (scanner.startsWith("<?")) {
      const end = scanner.indexOf("?>");
      if (end === -1) {
        throw new MalformedXmlError("Unterminated processing instruction");
      }
      scanner.moveTo(end + 2);
      scanner.skipWhitespace();
      continue;
    }
    if (scanner.startsWith("<!--")) {
      skipMisc(scanner);
      scanner.skipWhitespace();
      continue;
    }
    break;
  }

  if (scanner.atEnd || scanner.peek() !== "<") {
    throw new MalformedXmlError("XML document has no root element");
  }

  // DOCTYPE/ENTITY declarations are markup and may only appear in the
  // document prologue. Strip declarations/comments from the start of the
  // raw source to isolate that prologue, so the same letters inside CDATA
  // or text content do not false-positive.
  let prologue = source.replace(/^\uFEFF/, "").trimStart();
  for (;;) {
    if (prologue.startsWith("<!--")) {
      const end = prologue.indexOf("-->");
      prologue = (end === -1 ? "" : prologue.slice(end + 3)).trimStart();
    } else if (prologue.startsWith("<?")) {
      const end = prologue.indexOf("?>");
      prologue = (end === -1 ? "" : prologue.slice(end + 2)).trimStart();
    } else {
      break;
    }
  }
  if (prologue.startsWith("<!")) {
    throw new UnsafeXmlError(
      "DOCTYPE and ENTITY declarations are disabled; DTD processing is " +
        "not permitted",
    );
  }

  const root = readElement(scanner);
  scanner.skipWhitespace();
  while (!scanner.atEnd) {
    if (scanner.startsWith("<!--") || scanner.startsWith("<?")) {
      skipMisc(scanner);
      scanner.skipWhitespace();
      continue;
    }
    throw new MalformedXmlError(
      `Unexpected content after root element at offset ${scanner.offset}`,
    );
  }
  return root;
}

/** First descendant with the given local tag name, depth-first. */
export function findChild(
  element: XmlElement,
  tag: string,
): XmlElement | undefined {
  return element.children.find((child) => child.tag === tag);
}

/** All descendants with the given local tag name at any nesting depth. */
export function findAll(element: XmlElement, tag: string): XmlElement[] {
  const matches: XmlElement[] = [];
  const walk = (node: XmlElement): void => {
    for (const child of node.children) {
      if (child.tag === tag) {
        matches.push(child);
      }
      walk(child);
    }
  };
  walk(element);
  return matches;
}

/** Trimmed text content of a child element, or undefined when absent/empty. */
export function childText(
  element: XmlElement,
  tag: string,
): string | undefined {
  const child = findChild(element, tag);
  if (child === undefined) {
    return undefined;
  }
  const text = child.text.trim();
  return text === "" ? undefined : text;
}
