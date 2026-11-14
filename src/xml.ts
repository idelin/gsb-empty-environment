import { createHash } from "node:crypto";

export const SAML_PROTOCOL_NS = "urn:oasis:names:tc:SAML:2.0:protocol";
export const SAML_ASSERTION_NS = "urn:oasis:names:tc:SAML:2.0:assertion";
export const XMLDSIG_NS = "http://www.w3.org/2000/09/xmldsig#";
export const XSLT_NS = "http://www.w3.org/1999/XSL/Transform";
export const XML_NAMESPACE_NS = "http://www.w3.org/XML/1998/namespace";
export const EXCLUSIVE_C14N_NS = "http://www.w3.org/2001/10/xml-exc-c14n#";
const XMLNS_NAMESPACE_NS = "http://www.w3.org/2000/xmlns/";

const MAX_XML_BYTES = 5 * 1024 * 1024;
const MAX_DEPTH = 128;
const MAX_ATTRIBUTES = 128;
const MAX_ID_LENGTH = 256;
const PARSED_DOCUMENTS = new WeakSet<object>();

export interface RawByteRange {
  /** Inclusive UTF-8 byte offset in the original input. */
  readonly start: number;
  /** Exclusive UTF-8 byte offset in the original input. */
  readonly end: number;
}

export interface XmlAttribute {
  readonly kind: "attribute";
  readonly qName: string;
  readonly localName: string;
  readonly prefix: string | null;
  /** Empty string means that the attribute is unqualified. */
  readonly namespaceUri: string;
  readonly value: string;
  readonly rawRange: RawByteRange;
}

export interface XmlText {
  readonly kind: "text";
  readonly value: string;
  readonly cdata: boolean;
  readonly parent: XmlElement;
  readonly rawRange: RawByteRange;
}

export interface XmlComment {
  readonly kind: "comment";
  readonly value: string;
  readonly parent: XmlElement;
  readonly rawRange: RawByteRange;
}

export interface XmlProcessingInstruction {
  readonly kind: "processing-instruction";
  readonly target: string;
  readonly value: string;
  readonly parent: XmlElement;
  readonly rawRange: RawByteRange;
}

export type XmlChildNode = XmlElement | XmlText | XmlComment | XmlProcessingInstruction;

export interface XmlElement {
  readonly kind: "element";
  /** Stable identity assigned by this parser. Never derived from XML text. */
  readonly identity: number;
  readonly qName: string;
  readonly localName: string;
  readonly prefix: string | null;
  /** Empty string means that the element is in no namespace. */
  readonly namespaceUri: string;
  readonly attributes: readonly XmlAttribute[];
  readonly namespaceDeclarations: readonly XmlAttribute[];
  /** In-scope namespaces after applying declarations on this element. */
  readonly namespacesInScope: Readonly<Record<string, string>>;
  readonly children: readonly XmlChildNode[];
  readonly parent: XmlElement | null;
  readonly rawRange: RawByteRange;
  readonly idAttribute: XmlAttribute | null;
}

export interface SamlXmlDocument {
  readonly root: XmlElement;
  readonly idIndex: Readonly<Record<string, XmlElement>>;
  readonly sourceByteLength: number;
  readonly sourceSha256: string;
  readonly parserIdentityBase: number;
}

export class SamlXmlError extends Error {
  readonly code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = "SamlXmlError";
    this.code = code;
  }
}

interface MutableElement {
  kind: "element";
  identity: number;
  qName: string;
  localName: string;
  prefix: string | null;
  namespaceUri: string;
  attributes: XmlAttribute[];
  namespaceDeclarations: XmlAttribute[];
  namespacesInScope: Record<string, string>;
  children: XmlChildNode[];
  parent: MutableElement | null;
  rawRange: RawByteRange;
  idAttribute: XmlAttribute | null;
}

interface ParsedAttribute {
  readonly qName: string;
  readonly localName: string;
  readonly prefix: string | null;
  readonly value: string;
  readonly rawRange: RawByteRange;
}

function fail(code: string, message: string): never {
  throw new SamlXmlError(code, message);
}

function isXmlChar(codePoint: number): boolean {
  return (
    codePoint === 0x9 ||
    codePoint === 0xa ||
    codePoint === 0xd ||
    (codePoint >= 0x20 && codePoint <= 0xd7ff) ||
    (codePoint >= 0xe000 && codePoint <= 0xfffd) ||
    (codePoint >= 0x10000 && codePoint <= 0x10ffff)
  );
}

function decodeUtf8(bytes: Uint8Array): { text: string; byteOffsets: number[] } {
  let offset = 0;
  if (bytes.length >= 3 && bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf) {
    offset = 3;
  }

  const parts: string[] = [];
  const byteOffsets: number[] = [];
  let textLength = 0;

  const appendCodePoint = (codePoint: number, startByte: number, endByte: number): void => {
    if (!isXmlChar(codePoint)) {
      fail("XML_INVALID_CHARACTER", `Invalid XML character U+${codePoint.toString(16).toUpperCase()}`);
    }
    const character = String.fromCodePoint(codePoint);
    parts.push(character);
    for (let index = 0; index < character.length; index += 1) {
      byteOffsets[textLength + index] = startByte;
    }
    textLength += character.length;
    byteOffsets[textLength] = endByte;
  };

  while (offset < bytes.length) {
    const start = offset;
    const first = bytes[offset];
    let codePoint: number;

    if (first <= 0x7f) {
      codePoint = first;
      offset += 1;
    } else if (first >= 0xc2 && first <= 0xdf) {
      if (offset + 1 >= bytes.length || (bytes[offset + 1] & 0xc0) !== 0x80) {
        fail("XML_INVALID_UTF8", "Invalid UTF-8 sequence");
      }
      codePoint = ((first & 0x1f) << 6) | (bytes[offset + 1] & 0x3f);
      offset += 2;
    } else if (first >= 0xe0 && first <= 0xef) {
      if (
        offset + 2 >= bytes.length ||
        (bytes[offset + 1] & 0xc0) !== 0x80 ||
        (bytes[offset + 2] & 0xc0) !== 0x80
      ) {
        fail("XML_INVALID_UTF8", "Invalid UTF-8 sequence");
      }
      codePoint =
        ((first & 0x0f) << 12) |
        ((bytes[offset + 1] & 0x3f) << 6) |
        (bytes[offset + 2] & 0x3f);
      if (codePoint < 0x800 || (codePoint >= 0xd800 && codePoint <= 0xdfff)) {
        fail("XML_INVALID_UTF8", "Invalid UTF-8 sequence");
      }
      offset += 3;
    } else if (first >= 0xf0 && first <= 0xf4) {
      if (
        offset + 3 >= bytes.length ||
        (bytes[offset + 1] & 0xc0) !== 0x80 ||
        (bytes[offset + 2] & 0xc0) !== 0x80 ||
        (bytes[offset + 3] & 0xc0) !== 0x80
      ) {
        fail("XML_INVALID_UTF8", "Invalid UTF-8 sequence");
      }
      codePoint =
        ((first & 0x07) << 18) |
        ((bytes[offset + 1] & 0x3f) << 12) |
        ((bytes[offset + 2] & 0x3f) << 6) |
        (bytes[offset + 3] & 0x3f);
      if (codePoint < 0x10000 || codePoint > 0x10ffff) {
        fail("XML_INVALID_UTF8", "Invalid UTF-8 sequence");
      }
      offset += 4;
    } else {
      fail("XML_INVALID_UTF8", "Invalid UTF-8 sequence");
    }

    appendCodePoint(codePoint, start, offset);
  }

  byteOffsets[textLength] = offset;
  return { text: parts.join(""), byteOffsets };
}

function isNameStartCharacter(character: string): boolean {
  return /[A-Za-z_:]/.test(character) || /[\u00c0-\u02ff\u0370-\u1fff\u200c-\u200d\u2070-\u218f\u2c00-\u2fef\u3001-\ud7ff\uf900-\ufdcf\ufdf0-\ufffd]/.test(character);
}

function isNameCharacter(character: string): boolean {
  return isNameStartCharacter(character) || /[0-9.\-\u00b7\u0300-\u036f\u203f-\u2040]/.test(character);
}

function splitQName(qName: string): { prefix: string | null; localName: string } {
  const parts = qName.split(":");
  if (parts.length === 1) {
    return { prefix: null, localName: parts[0] };
  }
  if (parts.length !== 2 || parts[0].length === 0 || parts[1].length === 0) {
    fail("XML_INVALID_QNAME", `Invalid qualified name: ${qName}`);
  }
  return { prefix: parts[0], localName: parts[1] };
}

function decodeXmlValue(raw: string, attribute: boolean): string {
  let result = "";
  for (let index = 0; index < raw.length; index += 1) {
    const character = raw[index];

    if (character === "\r") {
      if (raw[index + 1] === "\n") {
        index += 1;
      }
      result += attribute ? " " : "\n";
      continue;
    }
    if (attribute && (character === "\n" || character === "\t")) {
      result += " ";
      continue;
    }
    if (character !== "&") {
      result += character;
      continue;
    }

    const semicolon = raw.indexOf(";", index + 1);
    if (semicolon < 0 || semicolon - index > 16) {
      fail("XML_INVALID_ENTITY", "Unterminated or unsupported entity reference");
    }
    const entity = raw.slice(index + 1, semicolon);
    let decoded: string;
    if (entity === "amp") {
      decoded = "&";
    } else if (entity === "lt") {
      decoded = "<";
    } else if (entity === "gt") {
      decoded = ">";
    } else if (entity === "quot") {
      decoded = '"';
    } else if (entity === "apos") {
      decoded = "'";
    } else if (entity.startsWith("#x")) {
      if (!/^#x[0-9A-Fa-f]+$/.test(entity)) {
        fail("XML_INVALID_ENTITY", `Invalid character reference: &${entity};`);
      }
      const codePoint = Number.parseInt(entity.slice(2), 16);
      if (!Number.isSafeInteger(codePoint) || !isXmlChar(codePoint)) {
        fail("XML_INVALID_ENTITY", `Invalid character reference: &${entity};`);
      }
      decoded = String.fromCodePoint(codePoint);
    } else if (entity.startsWith("#")) {
      if (!/^#[0-9]+$/.test(entity)) {
        fail("XML_INVALID_ENTITY", `Invalid character reference: &${entity};`);
      }
      const codePoint = Number.parseInt(entity.slice(1), 10);
      if (!Number.isSafeInteger(codePoint) || !isXmlChar(codePoint)) {
        fail("XML_INVALID_ENTITY", `Invalid character reference: &${entity};`);
      }
      decoded = String.fromCodePoint(codePoint);
    } else {
      fail("XML_ENTITY_FORBIDDEN", `Entity references are forbidden: &${entity};`);
    }

    result += decoded;
    index = semicolon;
  }
  return result;
}

function normalizeLineEndings(value: string): string {
  return value.replace(/\r\n?/g, "\n");
}

function isWhitespace(character: string): boolean {
  return character === " " || character === "\t" || character === "\n" || character === "\r";
}

function validateId(value: string): void {
  if (value.length === 0 || value.length > MAX_ID_LENGTH) {
    fail("XML_INVALID_ID", "ID must contain between 1 and 256 characters");
  }
  if (!/^[A-Za-z_][A-Za-z0-9_.:-]*$/.test(value)) {
    fail("XML_INVALID_ID", `Unsupported ID value: ${value}`);
  }
}

class SecureXmlParser {
  readonly text: string;
  readonly byteOffsets: number[];
  readonly bytes: Uint8Array;
  index = 0;
  identity = 0;
  root: MutableElement | null = null;
  readonly stack: MutableElement[] = [];

  constructor(bytes: Uint8Array) {
    this.bytes = bytes;
    const decoded = decodeUtf8(bytes);
    this.text = decoded.text;
    this.byteOffsets = decoded.byteOffsets;
  }

  byteAt(textIndex: number): number {
    const offset = this.byteOffsets[textIndex];
    if (offset === undefined) {
      fail("XML_INTERNAL_OFFSET", "XML parser produced an invalid byte offset");
    }
    return offset;
  }

  fail(code: string, message: string): never {
    return fail(code, `${message} at character ${this.index}`);
  }

  currentParent(): MutableElement | null {
    return this.stack.length === 0 ? null : this.stack[this.stack.length - 1];
  }

  appendChild(node: XmlChildNode): void {
    const parent = this.currentParent();
    if (parent === null) {
      return;
    }
    parent.children.push(node);
  }

  skipWhitespace(): void {
    while (this.index < this.text.length && isWhitespace(this.text[this.index])) {
      this.index += 1;
    }
  }

  readName(): string {
    const start = this.index;
    if (!isNameStartCharacter(this.text[this.index] ?? "")) {
      this.fail("XML_INVALID_NAME", "Expected an XML name");
    }
    this.index += 1;
    while (this.index < this.text.length && isNameCharacter(this.text[this.index])) {
      this.index += 1;
    }
    return this.text.slice(start, this.index);
  }

  parseDeclaredEncoding(declaration: string): void {
    const encodingMatch = /encoding\s*=\s*(['"])([^'"]+)\1/.exec(declaration);
    if (encodingMatch !== null && encodingMatch[2].toLowerCase() !== "utf-8") {
      this.fail("XML_ENCODING_FORBIDDEN", "Only UTF-8 XML input is accepted");
    }
  }

  parseXmlDeclaration(): void {
    if (!this.text.startsWith("<?xml", this.index)) {
      return;
    }
    const following = this.text[this.index + 5];
    if (following !== undefined && !isWhitespace(following) && following !== "?") {
      return;
    }
    if (this.index !== 0) {
      this.fail("XML_DECLARATION_POSITION", "XML declaration must be the first construct");
    }
    const end = this.text.indexOf("?>", this.index + 5);
    if (end < 0) {
      this.fail("XML_DECLARATION_UNTERMINATED", "Unterminated XML declaration");
    }
    this.parseDeclaredEncoding(this.text.slice(this.index, end + 2));
    this.index = end + 2;
  }

  parseComment(): XmlComment {
    const start = this.index;
    this.index += 4;
    const end = this.text.indexOf("-->", this.index);
    if (end < 0) {
      this.fail("XML_COMMENT_UNTERMINATED", "Unterminated XML comment");
    }
    const value = this.text.slice(this.index, end);
    if (value.includes("--") || value.endsWith("-")) {
      this.fail("XML_COMMENT_INVALID", "Invalid -- sequence in XML comment");
    }
    this.index = end + 3;
    const parent = this.currentParent();
    if (parent === null) {
      return {
        kind: "comment",
        value: normalizeLineEndings(value),
        parent: null as unknown as XmlElement,
        rawRange: Object.freeze({ start: this.byteAt(start), end: this.byteAt(this.index) }),
      };
    }
    return {
      kind: "comment",
      value: normalizeLineEndings(value),
      parent,
      rawRange: Object.freeze({ start: this.byteAt(start), end: this.byteAt(this.index) }),
    };
  }

  parseProcessingInstruction(): XmlProcessingInstruction {
    const start = this.index;
    this.index += 2;
    const target = this.readName();
    if (target.toLowerCase() === "xml") {
      this.fail("XML_DECLARATION_POSITION", "XML declaration is only allowed at the start");
    }
    if (target.toLowerCase() === "xml-stylesheet") {
      this.fail("XSLT_FORBIDDEN", "Stylesheet processing instructions are forbidden");
    }
    if (this.index < this.text.length && !isWhitespace(this.text[this.index])) {
      this.fail("XML_PI_INVALID", "Processing instruction target must be followed by whitespace");
    }
    const end = this.text.indexOf("?>", this.index);
    if (end < 0) {
      this.fail("XML_PI_UNTERMINATED", "Unterminated processing instruction");
    }
    let value = this.text.slice(this.index, end);
    if (value.startsWith(" ")) {
      value = value.slice(1);
    }
    this.index = end + 2;
    const parent = this.currentParent();
    return {
      kind: "processing-instruction",
      target,
      value: normalizeLineEndings(value),
      parent: parent as XmlElement,
      rawRange: Object.freeze({ start: this.byteAt(start), end: this.byteAt(this.index) }),
    };
  }

  parseCdata(): XmlText {
    const start = this.index;
    this.index += 9;
    const end = this.text.indexOf("]]>", this.index);
    if (end < 0) {
      this.fail("XML_CDATA_UNTERMINATED", "Unterminated CDATA section");
    }
    const value = this.text.slice(this.index, end);
    this.index = end + 3;
    const parent = this.currentParent();
    if (parent === null) {
      this.fail("XML_ROOT_REQUIRED", "CDATA is not allowed outside the root element");
    }
    return {
      kind: "text",
      value: normalizeLineEndings(value),
      cdata: true,
      parent,
      rawRange: Object.freeze({ start: this.byteAt(start), end: this.byteAt(this.index) }),
    };
  }

  parseText(): XmlText | null {
    const start = this.index;
    const end = this.text.indexOf("<", this.index);
    const stop = end < 0 ? this.text.length : end;
    const raw = this.text.slice(this.index, stop);
    this.index = stop;

    if (raw.includes("]]>")) {
      this.fail("XML_TEXT_INVALID", "The sequence ]]> is forbidden in text");
    }

    const parent = this.currentParent();
    if (parent === null) {
      if (raw.split("").some((character) => !isWhitespace(character))) {
        this.fail("XML_TEXT_OUTSIDE_ROOT", "Non-whitespace text is not allowed outside the root");
      }
      return null;
    }
    if (raw.length === 0) {
      return null;
    }
    return {
      kind: "text",
      value: decodeXmlValue(raw, false),
      cdata: false,
      parent,
      rawRange: Object.freeze({ start: this.byteAt(start), end: this.byteAt(this.index) }),
    };
  }

  parseAttributes(): ParsedAttribute[] {
    const attributes: ParsedAttribute[] = [];
    while (true) {
      this.skipWhitespace();
      if (this.text.startsWith("/>", this.index) || this.text.startsWith(">", this.index)) {
        return attributes;
      }
      if (this.index >= this.text.length) {
        this.fail("XML_TAG_UNTERMINATED", "Unterminated start tag");
      }
      const start = this.index;
      const qName = this.readName();
      this.skipWhitespace();
      if (this.text[this.index] !== "=") {
        this.fail("XML_ATTRIBUTE_INVALID", `Attribute ${qName} has no '='`);
      }
      this.index += 1;
      this.skipWhitespace();
      const quote = this.text[this.index];
      if (quote !== '"' && quote !== "'") {
        this.fail("XML_ATTRIBUTE_INVALID", `Attribute ${qName} value must be quoted`);
      }
      this.index += 1;
      const valueStart = this.index;
      const valueEnd = this.text.indexOf(quote, valueStart);
      if (valueEnd < 0) {
        this.fail("XML_ATTRIBUTE_UNTERMINATED", `Unterminated value for attribute ${qName}`);
      }
      const rawValue = this.text.slice(valueStart, valueEnd);
      if (rawValue.includes("<")) {
        this.fail("XML_ATTRIBUTE_INVALID", `Attribute ${qName} contains '<'`);
      }
      this.index = valueEnd + 1;
      const split = splitQName(qName);
      attributes.push({
        qName,
        ...split,
        value: decodeXmlValue(rawValue, true),
        rawRange: Object.freeze({ start: this.byteAt(start), end: this.byteAt(this.index) }),
      });
      if (attributes.length > MAX_ATTRIBUTES) {
        this.fail("XML_ATTRIBUTE_LIMIT", "Too many attributes");
      }
    }
  }

  parseStartTag(): void {
    const start = this.index;
    this.index += 1;
    const qName = this.readName();
    const parsedAttributes = this.parseAttributes();
    const selfClosing = this.text.startsWith("/>", this.index);
    if (selfClosing) {
      this.index += 2;
    } else if (this.text[this.index] === ">") {
      this.index += 1;
    } else {
      this.fail("XML_TAG_UNTERMINATED", `Unterminated start tag ${qName}`);
    }

    const parent = this.currentParent();
    if (parent === null && this.root !== null) {
      this.fail("XML_MULTIPLE_ROOTS", "Only one root element is allowed");
    }
    if (this.stack.length >= MAX_DEPTH) {
      this.fail("XML_DEPTH_LIMIT", "XML nesting is too deep");
    }

    const split = splitQName(qName);
    const namespacesInScope: Record<string, string> = Object.assign(
      Object.create(null) as Record<string, string>,
      parent === null ? {} : parent.namespacesInScope,
    );
    const namespaceDeclarations: XmlAttribute[] = [];
    const expandedAttributes = new Map<string, ParsedAttribute>();
    const declaredPrefixes = new Set<string>();

    for (const attribute of parsedAttributes) {
      const isDefaultDeclaration = attribute.qName === "xmlns";
      const isPrefixedDeclaration = attribute.prefix === "xmlns";
      if (!isDefaultDeclaration && !isPrefixedDeclaration) {
        const key = `${attribute.prefix ?? ""}\u0000${attribute.localName}`;
        if (expandedAttributes.has(key)) {
          this.fail("XML_DUPLICATE_ATTRIBUTE", `Duplicate attribute ${attribute.qName}`);
        }
        expandedAttributes.set(key, attribute);
        continue;
      }

      const declaredPrefix = isDefaultDeclaration ? "" : attribute.localName;
      if (declaredPrefixes.has(declaredPrefix)) {
        this.fail("XML_DUPLICATE_NAMESPACE", `Duplicate namespace declaration for ${declaredPrefix || "(default)"}`);
      }
      declaredPrefixes.add(declaredPrefix);
      if (declaredPrefix === "xmlns") {
        this.fail("XML_NAMESPACE_FORBIDDEN", "The xmlns prefix cannot be declared");
      }
      if (declaredPrefix === "xml" && attribute.value !== XML_NAMESPACE_NS) {
        this.fail("XML_NAMESPACE_FORBIDDEN", "The xml prefix has a fixed namespace");
      }
      if (attribute.value === XML_NAMESPACE_NS && declaredPrefix !== "xml") {
        this.fail("XML_NAMESPACE_FORBIDDEN", "Only the xml prefix may use the XML namespace");
      }
      if (attribute.value === XMLNS_NAMESPACE_NS) {
        this.fail("XML_NAMESPACE_FORBIDDEN", "The xmlns namespace cannot be bound to a prefix");
      }
      if (declaredPrefix !== "" && attribute.value.length === 0) {
        this.fail("XML_NAMESPACE_FORBIDDEN", "Undeleclaring a namespace prefix is forbidden");
      }
      if (attribute.value.length > 0) {
        namespacesInScope[declaredPrefix] = attribute.value;
      } else {
        delete namespacesInScope[declaredPrefix];
      }
      namespaceDeclarations.push({
        kind: "attribute",
        qName: attribute.qName,
        localName: attribute.localName,
        prefix: attribute.prefix,
        namespaceUri: XMLNS_NAMESPACE_NS,
        value: attribute.value,
        rawRange: attribute.rawRange,
      });
    }

    const resolvePrefix = (prefix: string | null): string => {
      if (prefix === null) {
        return namespacesInScope[""] ?? "";
      }
      if (prefix === "xml") {
        return XML_NAMESPACE_NS;
      }
      if (prefix === "xmlns") {
        this.fail("XML_NAMESPACE_FORBIDDEN", "The xmlns prefix cannot be used for elements or attributes");
      }
      const namespaceUri = namespacesInScope[prefix];
      if (namespaceUri === undefined || namespaceUri.length === 0) {
        this.fail("XML_UNBOUND_PREFIX", `Unbound namespace prefix: ${prefix}`);
      }
      return namespaceUri;
    };

    const attributes: XmlAttribute[] = [];
    const expandedAttributeNames = new Set<string>();
    let idAttribute: XmlAttribute | null = null;
    for (const parsed of expandedAttributes.values()) {
      if (parsed.localName.toLowerCase() === "id" && parsed.localName !== "ID") {
        this.fail("XML_ID_FORBIDDEN", `ID attribute spelling ${parsed.qName} is not accepted`);
      }
      if (parsed.localName === "ID" && parsed.prefix !== null) {
        this.fail("XML_ID_FORBIDDEN", "ID must be an unqualified attribute");
      }
      const namespaceUri = parsed.prefix === null ? "" : resolvePrefix(parsed.prefix);
      const expandedName = `${namespaceUri}\u0000${parsed.localName}`;
      if (expandedAttributeNames.has(expandedName)) {
        this.fail("XML_DUPLICATE_ATTRIBUTE", `Duplicate expanded attribute name: ${parsed.qName}`);
      }
      expandedAttributeNames.add(expandedName);
      const attribute: XmlAttribute = {
        kind: "attribute",
        qName: parsed.qName,
        localName: parsed.localName,
        prefix: parsed.prefix,
        namespaceUri,
        value: parsed.value,
        rawRange: parsed.rawRange,
      };
      attributes.push(attribute);
      if (parsed.localName === "ID") {
        validateId(parsed.value);
        if (idAttribute !== null) {
          this.fail("XML_DUPLICATE_ATTRIBUTE", "An element may contain only one ID attribute");
        }
        idAttribute = attribute;
      }
    }
    attributes.sort((left, right) => {
      if (left.namespaceUri < right.namespaceUri) return -1;
      if (left.namespaceUri > right.namespaceUri) return 1;
      if (left.localName < right.localName) return -1;
      if (left.localName > right.localName) return 1;
      return 0;
    });

    const namespaceUri = resolvePrefix(split.prefix);
    if (namespaceDeclarations.length > 0) {
      namespaceDeclarations.sort((left, right) => {
        const leftPrefix = left.qName === "xmlns" ? "" : left.localName;
        const rightPrefix = right.qName === "xmlns" ? "" : right.localName;
        return leftPrefix.localeCompare(rightPrefix);
      });
    }

    const element: MutableElement = {
      kind: "element",
      identity: ++this.identity,
      qName,
      ...split,
      namespaceUri,
      attributes,
      namespaceDeclarations: namespaceDeclarations.filter(
        (declaration) => !(declaration.qName === "xmlns:xml" && declaration.value === XML_NAMESPACE_NS),
      ),
      namespacesInScope: Object.freeze(
        Object.assign(Object.create(null) as Record<string, string>, namespacesInScope),
      ),
      children: [],
      parent,
      rawRange: Object.freeze({
        start: this.byteAt(start),
        end: this.byteAt(this.index),
      }),
      idAttribute,
    };

    if (parent === null) {
      this.root = element;
    } else {
      parent.children.push(element);
    }
    if (!selfClosing) {
      this.stack.push(element);
    }
  }

  parseEndTag(): void {
    this.index += 2;
    const qName = this.readName();
    this.skipWhitespace();
    if (this.text[this.index] !== ">") {
      this.fail("XML_END_TAG_INVALID", `Malformed end tag ${qName}`);
    }
    this.index += 1;
    const current = this.stack.pop();
    if (current === undefined || current.qName !== qName) {
      this.fail("XML_MISMATCHED_TAG", `Mismatched end tag ${qName}`);
    }
    current.rawRange = Object.freeze({ start: current.rawRange.start, end: this.byteAt(this.index) });
  }

  parse(): MutableElement {
    this.parseXmlDeclaration();
    while (this.index < this.text.length) {
      if (this.text.startsWith("<!--", this.index)) {
        const comment = this.parseComment();
        if (comment.parent !== null) {
          this.appendChild(comment);
        }
        continue;
      }
      if (this.text.startsWith("<![CDATA[", this.index)) {
        this.appendChild(this.parseCdata());
        continue;
      }
      if (this.text.startsWith("<?", this.index)) {
        const pi = this.parseProcessingInstruction();
        if (pi.parent !== null) {
          this.appendChild(pi);
        }
        continue;
      }
      if (this.text.startsWith("</", this.index)) {
        this.parseEndTag();
        continue;
      }
      if (this.text.startsWith("<!", this.index)) {
        this.fail("XML_DTD_FORBIDDEN", "DTD and entity declarations are forbidden");
      }
      if (this.text[this.index] === "<") {
        this.parseStartTag();
        continue;
      }
      const text = this.parseText();
      if (text !== null) {
        this.appendChild(text);
      }
    }

    if (this.stack.length !== 0) {
      this.fail("XML_TAG_UNTERMINATED", `Unclosed element ${this.stack[this.stack.length - 1].qName}`);
    }
    if (this.root === null) {
      this.fail("XML_ROOT_REQUIRED", "XML document has no root element");
    }
    return this.root;
  }
}

const NAMESPACE_LOCKED_ELEMENTS: Readonly<Record<string, string>> = Object.freeze(
  Object.assign(Object.create(null) as Record<string, string>, {
    Response: SAML_PROTOCOL_NS,
    Status: SAML_PROTOCOL_NS,
    StatusCode: SAML_PROTOCOL_NS,
    StatusMessage: SAML_PROTOCOL_NS,
    Assertion: SAML_ASSERTION_NS,
    Issuer: SAML_ASSERTION_NS,
    Subject: SAML_ASSERTION_NS,
    NameID: SAML_ASSERTION_NS,
    SubjectConfirmation: SAML_ASSERTION_NS,
    SubjectConfirmationData: SAML_ASSERTION_NS,
    Conditions: SAML_ASSERTION_NS,
    AudienceRestriction: SAML_ASSERTION_NS,
    Audience: SAML_ASSERTION_NS,
    AuthnStatement: SAML_ASSERTION_NS,
    AuthnContext: SAML_ASSERTION_NS,
    AuthnContextClassRef: SAML_ASSERTION_NS,
    Signature: XMLDSIG_NS,
    SignedInfo: XMLDSIG_NS,
    CanonicalizationMethod: XMLDSIG_NS,
    SignatureMethod: XMLDSIG_NS,
    Reference: XMLDSIG_NS,
    Transforms: XMLDSIG_NS,
    Transform: XMLDSIG_NS,
    DigestMethod: XMLDSIG_NS,
    DigestValue: XMLDSIG_NS,
    SignatureValue: XMLDSIG_NS,
    KeyInfo: XMLDSIG_NS,
    X509Data: XMLDSIG_NS,
    X509Certificate: XMLDSIG_NS,
    InclusiveNamespaces: EXCLUSIVE_C14N_NS,
  }),
);

function inspectTree(root: MutableElement): Record<string, XmlElement> {
  const ids: Record<string, XmlElement> = Object.create(null);
  const visit = (element: MutableElement): void => {
    if (element.namespaceUri === XSLT_NS) {
      fail("XSLT_FORBIDDEN", "XSLT elements are forbidden");
    }
    const requiredNamespace = NAMESPACE_LOCKED_ELEMENTS[element.localName];
    if (requiredNamespace !== undefined && element.namespaceUri !== requiredNamespace) {
      fail(
        "XML_NAMESPACE_CONFUSION",
        `${element.localName} must be in ${requiredNamespace}, got ${element.namespaceUri || "(none)"}`,
      );
    }
    if (element.idAttribute !== null) {
      const id = element.idAttribute.value;
      if (ids[id] !== undefined) {
        fail("XML_DUPLICATE_ID", `Duplicate ID in document: ${id}`);
      }
      ids[id] = element as unknown as XmlElement;
    }
    for (const child of element.children) {
      if (child.kind === "element") {
        visit(child);
      }
    }
  };
  visit(root);
  return ids;
}

function deepFreezeTree(element: MutableElement): XmlElement {
  for (const child of element.children) {
    if (child.kind === "element") {
      deepFreezeTree(child);
    } else {
      Object.freeze(child.rawRange);
      Object.freeze(child);
    }
  }
  for (const attribute of element.attributes) {
    Object.freeze(attribute.rawRange);
    Object.freeze(attribute);
  }
  for (const declaration of element.namespaceDeclarations) {
    Object.freeze(declaration.rawRange);
    Object.freeze(declaration);
  }
  Object.freeze(element.attributes);
  Object.freeze(element.namespaceDeclarations);
  Object.freeze(element.children);
  Object.freeze(element.rawRange);
  Object.freeze(element.namespacesInScope);
  Object.freeze(element);
  return element as unknown as XmlElement;
}

/**
 * Parses UTF-8 SAML XML while retaining stable node identities and byte ranges.
 * This is deliberately not a general-purpose XML parser: DTDs, custom entities,
 * stylesheets, namespace aliases around SAML/XMLDSig names, and duplicate IDs
 * are rejected before a tree is returned.
 */
export function parseSamlXml(rawXml: Uint8Array): SamlXmlDocument {
  if (!(rawXml instanceof Uint8Array) || rawXml.byteLength === 0) {
    fail("XML_INPUT_INVALID", "rawXml must be a non-empty Uint8Array");
  }
  if (rawXml.byteLength > MAX_XML_BYTES) {
    fail("XML_SIZE_LIMIT", `XML exceeds the ${MAX_XML_BYTES} byte limit`);
  }
  if (Buffer.from(rawXml.buffer, rawXml.byteOffset, rawXml.byteLength).includes("<!DOCTYPE")) {
    fail("XML_DTD_FORBIDDEN", "DOCTYPE declarations are forbidden");
  }

  const parser = new SecureXmlParser(rawXml);
  const mutableRoot = parser.parse();
  const ids = inspectTree(mutableRoot);
  const root = deepFreezeTree(mutableRoot);

  const frozenIds: Record<string, XmlElement> = Object.create(null);
  for (const [id, element] of Object.entries(ids)) {
    frozenIds[id] = element;
  }

  const document = Object.freeze({
    root,
    idIndex: Object.freeze(frozenIds),
    sourceByteLength: rawXml.byteLength,
    sourceSha256: createHash("sha256").update(rawXml).digest("hex"),
    parserIdentityBase: 1,
  });
  PARSED_DOCUMENTS.add(document);
  return document;
}

export function isParsedSamlXmlDocument(value: unknown): value is SamlXmlDocument {
  return (
    value !== null &&
    typeof value === "object" &&
    PARSED_DOCUMENTS.has(value) &&
    (value as SamlXmlDocument).root !== undefined
  );
}

export function findDirectChild(
  element: XmlElement,
  namespaceUri: string,
  localName: string,
): XmlElement | null {
  for (const child of element.children) {
    if (child.kind === "element" && child.namespaceUri === namespaceUri && child.localName === localName) {
      return child;
    }
  }
  return null;
}

export function findDirectChildren(
  element: XmlElement,
  namespaceUri: string,
  localName: string,
): readonly XmlElement[] {
  const result: XmlElement[] = [];
  for (const child of element.children) {
    if (child.kind === "element" && child.namespaceUri === namespaceUri && child.localName === localName) {
      result.push(child);
    }
  }
  return Object.freeze(result);
}

export function getUnqualifiedAttribute(element: XmlElement, localName: string): string | null {
  for (const attribute of element.attributes) {
    if (attribute.namespaceUri === "" && attribute.localName === localName) {
      return attribute.value;
    }
  }
  return null;
}

export function elementText(element: XmlElement): string {
  let result = "";
  for (const child of element.children) {
    if (child.kind === "text") {
      result += child.value;
    }
  }
  return result;
}
