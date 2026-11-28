import { XmlSecurityError } from './errors.ts';
import { decodeXmlInput, type DecodeResult, type WhitelistedEncoding } from './decode.ts';
import { parseDocumentType, type DtdMode, type GeneralEntity } from './dtd.ts';
import { detectEntityCycles, SafeEntityExpander, DEFAULT_ENTITY_QUOTAS, type EntityQuotas } from './entity.ts';
import { skipWhitespace, readName, skipComment, skipProcessingInstruction } from './xml-lex.ts';

export interface ScanOptions {
  dtdMode?: DtdMode;
  quotas?: Partial<EntityQuotas>;
  maxInputBytes?: number;
  maxElementDepth?: number;
}

export interface ScanResult {
  accepted: true;
  encoding: WhitelistedEncoding;
  hadBom: boolean;
  declaredEncoding?: string;
  elementCount: number;
  maxElementDepthObserved: number;
  namespaces: string[];
  totalReferenceChars: number;
  internalEntityCount: number;
}

const XINCLUDE_NS = 'http://www.w3.org/2001/XInclude';
const XSLT_NS = 'http://www.w3.org/1999/XSL/Transform';

interface Attribute {
  rawName: string;
  prefix: string;
  local: string;
  value: string;
  valueOffset: number;
}

interface NamespaceScope {
  prefix: string;
  uri: string;
}

const DEFAULT_MAX_INPUT_BYTES = 10 * 1024 * 1024;
const DEFAULT_MAX_ELEMENT_DEPTH = 1000;

export function inspectXml(input: Uint8Array, options: ScanOptions = {}): ScanResult {
  if (!(input instanceof Uint8Array)) {
    throw new XmlSecurityError('ENCODING_MALFORMED', 'input must be a Uint8Array of raw XML bytes');
  }
  const maxInputBytes = options.maxInputBytes ?? DEFAULT_MAX_INPUT_BYTES;
  if (input.length > maxInputBytes) {
    throw new XmlSecurityError(
      'INPUT_TOO_LARGE',
      `input of ${input.length} bytes exceeds limit of ${maxInputBytes}`,
    );
  }

  const decoded: DecodeResult = decodeXmlInput(input);
  const dtdMode: DtdMode = options.dtdMode ?? 'strict';
  const quotas: EntityQuotas = { ...DEFAULT_ENTITY_QUOTAS, ...options.quotas };
  const maxElementDepth = options.maxElementDepth ?? DEFAULT_MAX_ELEMENT_DEPTH;

  const scanner = new XmlScanner(decoded.text, dtdMode, quotas, maxElementDepth);
  const result = scanner.scan();

  return {
    accepted: true,
    encoding: decoded.encoding,
    hadBom: decoded.hadBom,
    declaredEncoding: decoded.declaredEncoding,
    elementCount: result.elementCount,
    maxElementDepthObserved: result.maxElementDepthObserved,
    namespaces: result.namespaces,
    totalReferenceChars: result.totalReferenceChars,
    internalEntityCount: result.internalEntityCount,
  };
}

export function safeInspectXml(input: Uint8Array, options: ScanOptions = {}): ScanResult | XmlSecurityError {
  try {
    return inspectXml(input, options);
  } catch (error) {
    if (error instanceof XmlSecurityError) {
      return error;
    }
    throw error;
  }
}

function splitQName(qname: string): { prefix: string; local: string } {
  const colon = qname.indexOf(':');
  if (colon < 0) {
    return { prefix: '', local: qname };
  }
  return { prefix: qname.slice(0, colon), local: qname.slice(colon + 1) };
}

class XmlScanner {
  private i = 0;
  private readonly nsStack: NamespaceScope[][] = [];
  private readonly namespaceUris = new Set<string>();
  private elementCount = 0;
  private maxDepthObserved = 0;
  private entities = new Map<string, GeneralEntity>();
  private readonly expander: SafeEntityExpander;
  private dtdConsumed = false;

  private readonly text: string;
  private readonly dtdMode: DtdMode;
  private readonly quotas: EntityQuotas;
  private readonly maxElementDepth: number;

  constructor(text: string, dtdMode: DtdMode, quotas: EntityQuotas, maxElementDepth: number) {
    this.text = text;
    this.dtdMode = dtdMode;
    this.quotas = quotas;
    this.maxElementDepth = maxElementDepth;
    this.expander = new SafeEntityExpander(this.entities, this.quotas);
  }

  scan(): {
    elementCount: number;
    maxElementDepthObserved: number;
    namespaces: string[];
    totalReferenceChars: number;
    internalEntityCount: number;
  } {
    this.i = skipWhitespace(this.text, 0);

    while (this.i < this.text.length) {
      this.i = skipWhitespace(this.text, this.i);
      if (this.text[this.i] !== '<') {
        throw new XmlSecurityError('XML_MALFORMED', 'unexpected content before root element', this.i);
      }
      if (this.text.startsWith('<!--', this.i)) {
        this.i = skipComment(this.text, this.i);
      } else if (this.text.startsWith('<?', this.i)) {
        this.i = skipProcessingInstruction(this.text, this.i);
      } else if (this.text.startsWith('<!DOCTYPE', this.i) || this.text.startsWith('<!doctype', this.i)) {
        this.consumeDtd();
      } else {
        break;
      }
    }

    if (this.i >= this.text.length) {
      throw new XmlSecurityError('XML_MALFORMED', 'document has no root element');
    }

    this.nsStack.push([]);
    this.parseElement();
    this.nsStack.pop();

    while (this.i < this.text.length) {
      this.i = skipWhitespace(this.text, this.i);
      if (this.i >= this.text.length) {
        break;
      }
      if (this.text.startsWith('<!--', this.i)) {
        this.i = skipComment(this.text, this.i);
      } else if (this.text.startsWith('<?', this.i)) {
        this.i = skipProcessingInstruction(this.text, this.i);
      } else {
        throw new XmlSecurityError('XML_MALFORMED', 'unexpected content after root element', this.i);
      }
    }

    return {
      elementCount: this.elementCount,
      maxElementDepthObserved: this.maxDepthObserved,
      namespaces: [...this.namespaceUris],
      totalReferenceChars: this.expander?.totalExpandedChars ?? 0,
      internalEntityCount: this.entities.size,
    };
  }

  private consumeDtd(): void {
    if (this.dtdConsumed) {
      throw new XmlSecurityError('DTD_MALFORMED', 'DOCTYPE is only allowed once in the prolog', this.i);
    }
    const parsed = parseDocumentType(this.text, this.i, this.dtdMode);
    this.i = parsed.next;
    for (const [name, entity] of parsed.result.entities) {
      this.entities.set(name, entity);
    }
    detectEntityCycles(this.entities);
    this.dtdConsumed = true;
  }

  private parseElement(): void {
    const startOffset = this.i;
    if (this.text[this.i] !== '<' || this.text[this.i + 1] === '/') {
      throw new XmlSecurityError('XML_MALFORMED', 'expected start tag', this.i);
    }
    this.i += 1;
    const tag = readName(this.text, this.i);
    if (tag.name === '') {
      throw new XmlSecurityError('XML_MALFORMED', 'missing element name', startOffset);
    }
    this.i = tag.next;
    const attributes = this.readAttributes();
    this.i = skipWhitespace(this.text, this.i);
    if (this.text[this.i] !== '>' && this.text[this.i] !== '/') {
      throw new XmlSecurityError('XML_MALFORMED', 'malformed start tag', this.i);
    }

    this.elementCount += 1;
    const depth = this.nsStack.length;
    if (depth > this.maxElementDepth) {
      throw new XmlSecurityError(
        'ELEMENT_DEPTH_EXCEEDED',
        `element nesting depth exceeded ${this.maxElementDepth}`,
        startOffset,
      );
    }
    this.maxDepthObserved = Math.max(this.maxDepthObserved, depth);

    const scope = this.buildNamespaceScope(attributes);
    this.nsStack.push(scope);

    const { prefix, local } = splitQName(tag.name);
    const namespaceUri = this.resolvePrefix(prefix, startOffset);
    this.enforceInjectionRules(namespaceUri, local, attributes, startOffset);
    this.verifyAttributeValues(attributes);

    if (this.text[this.i] === '/') {
      if (this.text[this.i + 1] !== '>') {
        throw new XmlSecurityError('XML_MALFORMED', 'malformed self-closing tag', this.i);
      }
      this.i += 2;
      this.nsStack.pop();
      return;
    }

    this.i += 1;
    this.parseContent(tag.name);
  }

  private parseContent(parentName: string): void {
    let textStart = this.i;
    while (this.i < this.text.length) {
      const ch = this.text[this.i];
      if (ch === '<') {
        if (this.text.startsWith('<!--', this.i)) {
          this.flushText(textStart);
          this.i = skipComment(this.text, this.i);
          textStart = this.i;
        } else if (this.text.startsWith('<![CDATA[', this.i)) {
          this.flushText(textStart);
          const end = this.text.indexOf(']]>', this.i + 9);
          if (end < 0) {
            throw new XmlSecurityError('XML_MALFORMED', 'unterminated CDATA section', this.i);
          }
          this.i = end + 3;
          textStart = this.i;
        } else if (this.text.startsWith('<?', this.i)) {
          this.flushText(textStart);
          this.i = skipProcessingInstruction(this.text, this.i);
          textStart = this.i;
        } else if (this.text[this.i + 1] === '/') {
          this.flushText(textStart);
          this.parseEndTag(parentName);
          return;
        } else {
          this.flushText(textStart);
          this.parseElement();
          textStart = this.i;
        }
      } else {
        this.i += 1;
      }
    }
    throw new XmlSecurityError('XML_MALFORMED', `unterminated element <${parentName}>`, textStart);
  }

  private flushText(textStart: number): void {
    if (this.i > textStart) {
      this.verifyReferences(this.text.slice(textStart, this.i), textStart);
    }
  }

  private parseEndTag(parentName: string): void {
    const start = this.i;
    this.i += 2;
    const tag = readName(this.text, this.i);
    if (tag.name === '' || tag.name !== parentName) {
      throw new XmlSecurityError('XML_MALFORMED', `mismatched end tag, expected </${parentName}>`, start);
    }
    this.i = tag.next;
    this.i = skipWhitespace(this.text, this.i);
    if (this.text[this.i] !== '>') {
      throw new XmlSecurityError('XML_MALFORMED', 'malformed end tag', this.i);
    }
    this.i += 1;
    this.nsStack.pop();
  }

  private readAttributes(): Attribute[] {
    const attributes: Attribute[] = [];
    const seen = new Set<string>();
    while (true) {
      this.i = skipWhitespace(this.text, this.i);
      const ch = this.text[this.i];
      if (ch === '>' || ch === '/' || ch === undefined) {
        return attributes;
      }
      const start = this.i;
      const name = readName(this.text, this.i);
      if (name.name === '') {
        throw new XmlSecurityError('XML_MALFORMED', 'invalid attribute name', start);
      }
      if (seen.has(name.name)) {
        throw new XmlSecurityError('XML_MALFORMED', `duplicate attribute "${name.name}"`, start);
      }
      seen.add(name.name);
      this.i = name.next;
      this.i = skipWhitespace(this.text, this.i);
      if (this.text[this.i] !== '=') {
        throw new XmlSecurityError('XML_MALFORMED', 'attribute value is required', this.i);
      }
      this.i += 1;
      this.i = skipWhitespace(this.text, this.i);
      const quote = this.text[this.i];
      if (quote !== '"' && quote !== "'") {
        throw new XmlSecurityError('XML_MALFORMED', 'attribute values must be quoted', this.i);
      }
      const valueStart = this.i + 1;
      const valueEnd = this.text.indexOf(quote, valueStart);
      if (valueEnd < 0) {
        throw new XmlSecurityError('XML_MALFORMED', 'unterminated attribute value', start);
      }
      const value = this.text.slice(valueStart, valueEnd);
      this.i = valueEnd + 1;
      const { prefix, local } = splitQName(name.name);
      attributes.push({ rawName: name.name, prefix, local, value, valueOffset: valueStart });
    }
  }

  private buildNamespaceScope(attributes: Attribute[]): NamespaceScope[] {
    const scope: NamespaceScope[] = [];
    for (const attribute of attributes) {
      if (attribute.prefix === 'xmlns') {
        scope.push({ prefix: attribute.local, uri: attribute.value });
        this.namespaceUris.add(attribute.value);
      } else if (attribute.rawName === 'xmlns') {
        scope.push({ prefix: '', uri: attribute.value });
        this.namespaceUris.add(attribute.value);
      }
    }
    return scope;
  }

  private resolvePrefix(prefix: string, offset: number): string {
    if (prefix === 'xml') {
      return 'http://www.w3.org/XML/1998/namespace';
    }
    if (prefix === 'xmlns') {
      return 'http://www.w3.org/2000/xmlns/';
    }
    for (let depth = this.nsStack.length - 1; depth >= 0; depth--) {
      const frame = this.nsStack[depth] as NamespaceScope[];
      for (let k = frame.length - 1; k >= 0; k--) {
        const binding = frame[k] as NamespaceScope;
        if (binding.prefix === prefix) {
          return binding.uri;
        }
      }
    }
    if (prefix === '') {
      return '';
    }
    throw new XmlSecurityError(
      'NAMESPACE_PREFIX_UNDECLARED',
      `namespace prefix "${prefix}" is used but never declared`,
      offset,
    );
  }

  private enforceInjectionRules(
    namespaceUri: string,
    local: string,
    attributes: Attribute[],
    offset: number,
  ): void {
    if (namespaceUri === XINCLUDE_NS && local === 'include') {
      throw new XmlSecurityError(
        'XINCLUDE_BLOCKED',
        'XInclude <include> external resource loading is blocked regardless of prefix name',
        offset,
      );
    }

    if (namespaceUri === XSLT_NS && (local === 'import' || local === 'include')) {
      throw new XmlSecurityError(
        'XSLT_IMPORT_BLOCKED',
        `XSLT <${local}> external stylesheet loading is blocked`,
        offset,
      );
    }

    if (namespaceUri === XSLT_NS) {
      for (const attribute of attributes) {
        if (containsDocumentCall(attribute.value)) {
          throw new XmlSecurityError(
            'XSLT_DOCUMENT_BLOCKED',
            `XSLT document() external resource call is blocked in attribute "${attribute.rawName}"`,
            attribute.valueOffset,
          );
        }
      }
    }
  }

  private verifyAttributeValues(attributes: Attribute[]): void {
    for (const attribute of attributes) {
      if (attribute.value.includes('&')) {
        this.verifyReferences(attribute.value, attribute.valueOffset);
      }
    }
  }

  private verifyReferences(text: string, offset: number): void {
    if (!text.includes('&')) {
      return;
    }
    try {
      this.expander.verify(text);
    } catch (error) {
      if (error instanceof XmlSecurityError && error.offset !== undefined) {
        throw new XmlSecurityError(error.code, error.message.replace(/ \(offset \d+\)$/, ''), error.offset + offset);
      }
      throw error;
    }
  }
}

/**
 * 仅在 XSLT 元素的属性值上匹配 document( 调用，允许空白与大小写变体。
 */
function containsDocumentCall(value: string): boolean {
  return /document\s*\(/i.test(value);
}
