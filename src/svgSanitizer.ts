const SVG_NAMESPACE = "http://www.w3.org/2000/svg";
const XLINK_NAMESPACE = "http://www.w3.org/1999/xlink";

const DEFAULT_MAX_BYTES = 2 * 1024 * 1024;
const DEFAULT_MAX_DEPTH = 64;
const DEFAULT_MAX_NODES = 10_000;
const MAX_ID_LENGTH = 256;
const MAX_SELECTOR_LENGTH = 4_096;
const MAX_DECLARATION_LENGTH = 65_536;

export interface SanitizeOptions {
  /** Reject inputs larger than this many UTF-8 bytes. */
  maxBytes?: number;
  /** Maximum element nesting depth, with the root SVG at depth 1. */
  maxDepth?: number;
  /** Maximum number of input elements, including elements later removed. */
  maxNodes?: number;
}

export interface SvgSanitizationIssue {
  code:
    | "ELEMENT_REMOVED"
    | "ATTRIBUTE_REMOVED"
    | "REFERENCE_REMOVED"
    | "STYLE_REMOVED";
  message: string;
  element?: string;
  attribute?: string;
}

export interface SvgSanitizationResult {
  svg: string;
  issues: SvgSanitizationIssue[];
}

export class SvgSanitizationError extends Error {
  readonly code: string;
  readonly offset?: number;

  constructor(code: string, message: string, offset?: number) {
    super(message);
    this.name = "SvgSanitizationError";
    this.code = code;
    this.offset = offset;
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

export class SvgLimitError extends SvgSanitizationError {
  constructor(code: string, message: string, offset?: number) {
    super(code, message, offset);
    this.name = "SvgLimitError";
  }
}

interface XmlAttribute {
  name: string;
  localName: string;
  prefix?: string;
  value: string;
}

interface XmlElement {
  type: "element";
  name: string;
  localName: string;
  attributes: XmlAttribute[];
  children: XmlNode[];
}

interface XmlText {
  type: "text";
  value: string;
}

type XmlNode = XmlElement | XmlText;

interface CleanAttribute {
  name: string;
  localName: string;
  value: string;
}

interface CleanElement {
  type: "element";
  name: string;
  attributes: CleanAttribute[];
  children: CleanNode[];
}

interface CleanText {
  type: "text";
  value: string;
}

type CleanNode = CleanElement | CleanText;

interface ReferenceEdge {
  element: CleanElement;
  attribute: CleanAttribute;
  sourceId: string;
  targetId: string;
}

interface GraphLink {
  sourceId: string;
  targetId: string;
}

const ALLOWED_ELEMENTS = new Set([
  "svg",
  "g",
  "defs",
  "symbol",
  "use",
  "path",
  "rect",
  "circle",
  "ellipse",
  "line",
  "polyline",
  "polygon",
  "text",
  "tspan",
  "textPath",
  "title",
  "desc",
  "a",
  "switch",
  "view",
  "style",
  "marker",
  "pattern",
  "clipPath",
  "mask",
  "linearGradient",
  "radialGradient",
  "stop",
  "filter",
  "feBlend",
  "feColorMatrix",
  "feComponentTransfer",
  "feComposite",
  "feConvolveMatrix",
  "feDiffuseLighting",
  "feDisplacementMap",
  "feDistantLight",
  "feDropShadow",
  "feFlood",
  "feFuncA",
  "feFuncB",
  "feFuncG",
  "feFuncR",
  "feGaussianBlur",
  "feImage",
  "feMerge",
  "feMergeNode",
  "feMorphology",
  "feOffset",
  "fePointLight",
  "feSpecularLighting",
  "feSpotLight",
  "feTile",
  "feTurbulence",
  "solidColor",
]);

const ALLOWED_ATTRIBUTES = new Set([
  "id",
  "class",
  "lang",
  "space",
  "role",
  "tabindex",
  "focusable",
  "style",
  "transform",
  "transform-origin",
  "version",
  "baseProfile",
  "viewBox",
  "preserveAspectRatio",
  "width",
  "height",
  "x",
  "y",
  "x1",
  "y1",
  "x2",
  "y2",
  "cx",
  "cy",
  "r",
  "rx",
  "ry",
  "fx",
  "fy",
  "fr",
  "d",
  "pathLength",
  "points",
  "dx",
  "dy",
  "rotate",
  "textLength",
  "lengthAdjust",
  "startOffset",
  "method",
  "spacing",
  "side",
  "dominant-baseline",
  "alignment-baseline",
  "baseline-shift",
  "direction",
  "unicode-bidi",
  "writing-mode",
  "glyph-orientation-horizontal",
  "glyph-orientation-vertical",
  "fill",
  "fill-opacity",
  "fill-rule",
  "stroke",
  "stroke-width",
  "stroke-linecap",
  "stroke-linejoin",
  "stroke-miterlimit",
  "stroke-dasharray",
  "stroke-dashoffset",
  "stroke-opacity",
  "color",
  "color-interpolation",
  "color-interpolation-filters",
  "color-rendering",
  "paint-order",
  "shape-rendering",
  "vector-effect",
  "opacity",
  "visibility",
  "display",
  "overflow",
  "clip",
  "clip-rule",
  "clip-path",
  "mask",
  "filter",
  "marker",
  "marker-start",
  "marker-mid",
  "marker-end",
  "cursor",
  "font-family",
  "font-size",
  "font-size-adjust",
  "font-stretch",
  "font-style",
  "font-variant",
  "font-weight",
  "kerning",
  "letter-spacing",
  "word-spacing",
  "text-anchor",
  "text-decoration",
  "text-rendering",
  "gradientUnits",
  "gradientTransform",
  "spreadMethod",
  "offset",
  "stop-color",
  "stop-opacity",
  "patternUnits",
  "patternContentUnits",
  "patternTransform",
  "clipPathUnits",
  "maskUnits",
  "maskContentUnits",
  "mask-type",
  "filterUnits",
  "primitiveUnits",
  "requiredFeatures",
  "requiredExtensions",
  "systemLanguage",
  "externalResourcesRequired",
  "enable-background",
  "in",
  "in2",
  "result",
  "stdDeviation",
  "operator",
  "k1",
  "k2",
  "k3",
  "k4",
  "tableValues",
  "slope",
  "intercept",
  "amplitude",
  "exponent",
  "bias",
  "scale",
  "seed",
  "numOctaves",
  "stitchTiles",
  "baseFrequency",
  "type",
  "values",
  "edgeMode",
  "kernelMatrix",
  "kernelUnitLength",
  "order",
  "divisor",
  "targetX",
  "targetY",
  "preserveAlpha",
  "surfaceScale",
  "diffuseConstant",
  "specularConstant",
  "specularExponent",
  "limitingConeAngle",
  "radius",
  "azimuth",
  "elevation",
  "pointsAtX",
  "pointsAtY",
  "pointsAtZ",
  "xChannelSelector",
  "yChannelSelector",
  "z",
  "mode",
  "flood-color",
  "flood-opacity",
  "lighting-color",
  "refX",
  "refY",
  "markerWidth",
  "markerHeight",
  "orient",
  "href",
  "paint-order",
  "solid-color",
  "solid-opacity",
]);

const URL_BEARING_ATTRIBUTES = new Set([
  "fill",
  "stroke",
  "filter",
  "clip-path",
  "mask",
  "marker",
  "marker-start",
  "marker-mid",
  "marker-end",
  "cursor",
]);

const DANGEROUS_CSS_PROPERTIES = new Set([
  "behavior",
  "-moz-binding",
  "binding",
]);

const XML_NAME = /^[A-Za-z_][A-Za-z0-9_.:-]*$/;
const SAFE_ID = /^[A-Za-z0-9_.:-]+$/;

function isElement(node: XmlNode | CleanNode): node is XmlElement | CleanElement {
  return node.type === "element";
}

function isValidXmlCodePoint(codePoint: number): boolean {
  return (
    codePoint === 0x9 ||
    codePoint === 0xa ||
    codePoint === 0xd ||
    (codePoint >= 0x20 && codePoint <= 0xd7ff) ||
    (codePoint >= 0xe000 && codePoint <= 0xfffd) ||
    (codePoint >= 0x10000 && codePoint <= 0x10ffff)
  );
}

function decodeXmlEntities(
  value: string,
  baseOffset: number,
): string {
  if (!value.includes("&")) {
    return value;
  }

  let output = "";
  for (let index = 0; index < value.length; index += 1) {
    const character = value[index];
    if (character !== "&") {
      output += character;
      continue;
    }

    const semicolon = value.indexOf(";", index + 1);
    if (semicolon < 0 || semicolon - index > 32) {
      throw new SvgSanitizationError(
        "INVALID_XML",
        "Unterminated or invalid XML entity reference",
        baseOffset + index,
      );
    }

    const entity = value.slice(index + 1, semicolon);
    if (entity === "amp") {
      output += "&";
    } else if (entity === "lt") {
      output += "<";
    } else if (entity === "gt") {
      output += ">";
    } else if (entity === "quot") {
      output += '"';
    } else if (entity === "apos") {
      output += "'";
    } else if (/^#x[0-9a-fA-F]+$/.test(entity)) {
      const codePoint = Number.parseInt(entity.slice(2), 16);
      if (!isValidXmlCodePoint(codePoint)) {
        throw new SvgSanitizationError(
          "INVALID_XML",
          "Numeric character reference is not valid XML",
          baseOffset + index,
        );
      }
      output += String.fromCodePoint(codePoint);
    } else if (/^#[0-9]+$/.test(entity)) {
      const codePoint = Number.parseInt(entity.slice(1), 10);
      if (!isValidXmlCodePoint(codePoint)) {
        throw new SvgSanitizationError(
          "INVALID_XML",
          "Numeric character reference is not valid XML",
          baseOffset + index,
        );
      }
      output += String.fromCodePoint(codePoint);
    } else {
      throw new SvgSanitizationError(
        "EXTERNAL_ENTITY",
        `Unsupported entity reference &${entity};`,
        baseOffset + index,
      );
    }

    index = semicolon;
  }

  return output;
}

class StrictXmlParser {
  readonly input: string;
  readonly maxDepth: number;
  readonly maxNodes: number;
  position = 0;
  nodeCount = 0;

  constructor(input: string, maxDepth: number, maxNodes: number) {
    this.input = input;
    this.maxDepth = maxDepth;
    this.maxNodes = maxNodes;
  }

  parse(): XmlElement {
    this.skipMisc();
    if (this.isExternalMarkupDeclaration()) {
      throw this.error(
        "EXTERNAL_ENTITY",
        "DOCTYPE declarations and external entities are not allowed",
      );
    }
    if (this.input[this.position] !== "<") {
      throw this.error("INVALID_XML", "Expected an SVG root element");
    }

    const root = this.parseElement(1);
    this.skipMisc();
    if (this.isExternalMarkupDeclaration()) {
      throw this.error(
        "EXTERNAL_ENTITY",
        "DOCTYPE declarations and external entities are not allowed",
      );
    }
    if (this.position !== this.input.length) {
      throw this.error("INVALID_XML", "Unexpected content after the root element");
    }
    return root;
  }

  private parseElement(depth: number): XmlElement {
    if (depth > this.maxDepth) {
      throw new SvgLimitError(
        "MAX_DEPTH_EXCEEDED",
        `SVG nesting depth ${depth} exceeds the limit of ${this.maxDepth}`,
        this.position,
      );
    }

    this.nodeCount += 1;
    if (this.nodeCount > this.maxNodes) {
      throw new SvgLimitError(
        "MAX_NODES_EXCEEDED",
        `SVG node count exceeds the limit of ${this.maxNodes}`,
        this.position,
      );
    }

    this.expect("<");
    const name = this.readName();
    const attributes: XmlAttribute[] = [];
    const seenAttributes = new Set<string>();

    while (true) {
      this.skipWhitespace();
      if (this.startsWith("/>")) {
        this.position += 2;
        return {
          type: "element",
          name,
          localName: localName(name),
          attributes,
          children: [],
        };
      }
      if (this.input[this.position] === ">") {
        this.position += 1;
        break;
      }
      if (this.position >= this.input.length) {
        throw this.error("INVALID_XML", `Unclosed element <${name}>`);
      }

      const attributeStart = this.position;
      const attributeName = this.readName();
      if (seenAttributes.has(attributeName)) {
        throw this.error(
          "INVALID_XML",
          `Duplicate attribute ${attributeName}`,
          attributeStart,
        );
      }
      seenAttributes.add(attributeName);

      this.skipWhitespace();
      this.expect("=");
      this.skipWhitespace();
      const quote = this.input[this.position];
      if (quote !== '"' && quote !== "'") {
        throw this.error("INVALID_XML", "Attribute values must be quoted");
      }
      this.position += 1;
      const valueStart = this.position;
      const valueEnd = this.input.indexOf(quote, valueStart);
      if (valueEnd < 0) {
        throw this.error("INVALID_XML", "Unterminated attribute value");
      }
      const rawValue = this.input.slice(valueStart, valueEnd);
      if (rawValue.includes("<")) {
        throw this.error(
          "INVALID_XML",
          "Attribute values cannot contain a raw '<'",
          valueStart,
        );
      }
      this.position = valueEnd + 1;

      const colon = attributeName.indexOf(":");
      attributes.push({
        name: attributeName,
        localName: localName(attributeName),
        prefix: colon >= 0 ? attributeName.slice(0, colon) : undefined,
        value: decodeXmlEntities(rawValue, valueStart),
      });
    }

    const children: XmlNode[] = [];
    while (true) {
      if (this.position >= this.input.length) {
        throw this.error("INVALID_XML", `Unclosed element <${name}>`);
      }

      if (this.startsWith("</")) {
        this.position += 2;
        const closingName = this.readName();
        if (closingName !== name) {
          throw this.error(
            "INVALID_XML",
            `Mismatched closing tag </${closingName}> for <${name}>`,
          );
        }
        this.skipWhitespace();
        this.expect(">");
        break;
      }

      if (this.startsWith("<!--")) {
        this.skipComment();
        continue;
      }
      if (this.startsWith("<![CDATA[")) {
        children.push({ type: "text", value: this.readCdata() });
        continue;
      }
      if (this.startsWith("<?")) {
        this.skipProcessingInstruction();
        continue;
      }
      if (this.startsWith("<!")) {
        throw this.error(
          "EXTERNAL_ENTITY",
          "DTD and entity declarations are not allowed",
        );
      }
      if (this.input[this.position] === "<") {
        children.push(this.parseElement(depth + 1));
        continue;
      }

      const textStart = this.position;
      const nextTag = this.input.indexOf("<", textStart);
      const textEnd = nextTag < 0 ? this.input.length : nextTag;
      const rawText = this.input.slice(textStart, textEnd);
      children.push({
        type: "text",
        value: decodeXmlEntities(rawText, textStart),
      });
      this.position = textEnd;
    }

    return {
      type: "element",
      name,
      localName: localName(name),
      attributes,
      children,
    };
  }

  private skipMisc(): void {
    while (this.position < this.input.length) {
      this.skipWhitespace();
      if (this.startsWith("<!--")) {
        this.skipComment();
      } else if (this.startsWith("<?")) {
        this.skipProcessingInstruction();
      } else {
        break;
      }
    }
  }

  private skipWhitespace(): void {
    while (
      this.position < this.input.length &&
      /[ \t\r\n]/.test(this.input[this.position])
    ) {
      this.position += 1;
    }
  }

  private skipComment(): void {
    const end = this.input.indexOf("-->", this.position + 4);
    if (end < 0) {
      throw this.error("INVALID_XML", "Unterminated XML comment");
    }
    this.position = end + 3;
  }

  private skipProcessingInstruction(): void {
    const end = this.input.indexOf("?>", this.position + 2);
    if (end < 0) {
      throw this.error(
        "INVALID_XML",
        "Unterminated XML processing instruction",
      );
    }
    this.position = end + 2;
  }

  private readCdata(): string {
    const end = this.input.indexOf("]]>", this.position + 9);
    if (end < 0) {
      throw this.error("INVALID_XML", "Unterminated CDATA section");
    }
    const value = this.input.slice(this.position + 9, end);
    this.position = end + 3;
    return value;
  }

  private readName(): string {
    const start = this.position;
    while (
      this.position < this.input.length &&
      !/[ \t\r\n=/>]/.test(this.input[this.position])
    ) {
      this.position += 1;
    }
    const name = this.input.slice(start, this.position);
    if (!XML_NAME.test(name)) {
      throw this.error("INVALID_XML", `Invalid XML name: ${name}`, start);
    }
    return name;
  }

  private expect(value: string): void {
    if (!this.startsWith(value)) {
      throw this.error("INVALID_XML", `Expected '${value}'`);
    }
    this.position += value.length;
  }

  private startsWith(value: string): boolean {
    return this.input.startsWith(value, this.position);
  }

  private isExternalMarkupDeclaration(): boolean {
    const declaration = this.input
      .slice(this.position, this.position + 9)
      .toUpperCase();
    return (
      declaration.startsWith("<!DOCTYPE") ||
      declaration.startsWith("<!ENTITY")
    );
  }

  private error(code: string, message: string, offset = this.position): SvgSanitizationError {
    return new SvgSanitizationError(code, message, offset);
  }
}

function localName(name: string): string {
  const colon = name.indexOf(":");
  return colon >= 0 ? name.slice(colon + 1) : name;
}

function removeAttribute(element: CleanElement, attribute: CleanAttribute): void {
  const index = element.attributes.indexOf(attribute);
  if (index >= 0) {
    element.attributes.splice(index, 1);
  }
}

function getAttribute(
  element: CleanElement,
  localNameToFind: string,
): CleanAttribute | undefined {
  return element.attributes.find(
    (attribute) => attribute.localName === localNameToFind,
  );
}

function isSafeId(value: string, safeIds: ReadonlyMap<string, CleanElement>): boolean {
  return (
    value.length > 0 &&
    value.length <= MAX_ID_LENGTH &&
    SAFE_ID.test(value) &&
    safeIds.has(value)
  );
}

function stripCssComments(value: string): string | null {
  let output = "";
  let index = 0;
  let quote = "";

  while (index < value.length) {
    const character = value[index];
    if (quote) {
      output += character;
      if (character === "\\") {
        if (index + 1 < value.length) {
          output += value[index + 1];
          index += 2;
          continue;
        }
      } else if (character === quote) {
        quote = "";
      }
      index += 1;
      continue;
    }

    if (character === '"' || character === "'") {
      quote = character;
      output += character;
      index += 1;
      continue;
    }

    if (value.startsWith("/*", index)) {
      const end = value.indexOf("*/", index + 2);
      if (end < 0) {
        return null;
      }
      index = end + 2;
      continue;
    }

    output += character;
    index += 1;
  }

  return output;
}

function decodeCssEscapes(value: string): string {
  return value.replace(
    /\\([0-9a-fA-F]{1,6})(?:[ \t\r\n\f])?|\\([\s\S])/g,
    (_match, hex: string | undefined, escaped: string | undefined) => {
      if (hex !== undefined) {
        const codePoint = Number.parseInt(hex, 16);
        return isValidXmlCodePoint(codePoint)
          ? String.fromCodePoint(codePoint)
          : "\uFFFD";
      }
      if (escaped === "\n" || escaped === "\r" || escaped === "\f") {
        return "";
      }
      return escaped ?? "";
    },
  );
}

function normalizedCss(value: string): string {
  return decodeCssEscapes(value)
    .toLowerCase()
    .replace(/[\u0000-\u0020\u007f-\u009f]+/g, "");
}

function containsExternalCss(value: string): boolean {
  const normalized = normalizedCss(value);
  return (
    normalized.includes("javascript:") ||
    normalized.includes("vbscript:") ||
    normalized.includes("data:") ||
    normalized.includes("file:") ||
    normalized.includes("http:") ||
    normalized.includes("https:") ||
    normalized.includes("ftp:") ||
    normalized.includes("blob:") ||
    normalized.includes("//") ||
    normalized.includes("@import") ||
    normalized.includes("@namespace") ||
    normalized.includes("@font-face") ||
    normalized.includes("expression(") ||
    normalized.includes("progid:") ||
    normalized.includes("-moz-binding") ||
    normalized.includes("behavior:") ||
    normalized.includes("image(") ||
    normalized.includes("imageset(") ||
    normalized.includes("-webkit-image-set(") ||
    normalized.includes("cross-fade(") ||
    normalized.includes("src(")
  );
}

interface ParsedUrl {
  argument: string;
  start: number;
  end: number;
}

function parseCssUrls(value: string): ParsedUrl[] | null {
  const urls: ParsedUrl[] = [];
  const lower = value.toLowerCase();
  let index = 0;

  while (index < value.length) {
    const found = lower.indexOf("url", index);
    if (found < 0) {
      break;
    }
    const before = found === 0 ? "" : value[found - 1];
    if (before && /[A-Za-z0-9_.-]/.test(before)) {
      index = found + 3;
      continue;
    }

    let open = found + 3;
    while (open < value.length && /[ \t\r\n\f]/.test(value[open])) {
      open += 1;
    }
    if (value[open] !== "(") {
      index = found + 3;
      continue;
    }

    let cursor = open + 1;
    let quote = "";
    let depth = 1;
    while (cursor < value.length) {
      const character = value[cursor];
      if (quote) {
        if (character === "\\") {
          cursor += 2;
          continue;
        }
        if (character === quote) {
          quote = "";
        }
        cursor += 1;
        continue;
      }
      if (character === '"' || character === "'") {
        quote = character;
        cursor += 1;
        continue;
      }
      if (character === "(") {
        depth += 1;
      } else if (character === ")") {
        depth -= 1;
        if (depth === 0) {
          break;
        }
      }
      cursor += 1;
    }

    if (depth !== 0 || quote) {
      return null;
    }

    urls.push({
      argument: value.slice(open + 1, cursor).trim(),
      start: found,
      end: cursor + 1,
    });
    index = cursor + 1;
  }

  return urls;
}

function unquoteCss(value: string): string {
  const trimmed = value.trim();
  if (
    trimmed.length >= 2 &&
    ((trimmed.startsWith('"') && trimmed.endsWith('"')) ||
      (trimmed.startsWith("'") && trimmed.endsWith("'")))
  ) {
    return trimmed.slice(1, -1);
  }
  return trimmed;
}

function validateCssUrls(
  value: string,
  safeIds: ReadonlyMap<string, CleanElement>,
): boolean {
  const urls = parseCssUrls(value);
  if (urls === null) {
    return false;
  }

  for (const url of urls) {
    const argument = unquoteCss(url.argument);
    if (!argument.startsWith("#")) {
      return false;
    }
    const id = argument.slice(1);
    if (!isSafeId(id, safeIds)) {
      return false;
    }
  }

  return urls.length === 0 || !containsExternalCss(value);
}

function sanitizeCssValue(
  value: string,
  safeIds: ReadonlyMap<string, CleanElement>,
): string | null {
  if (!value || value.length > MAX_DECLARATION_LENGTH) {
    return null;
  }
  if (value.includes("{") || value.includes("}")) {
    return null;
  }
  if (containsExternalCss(value)) {
    return null;
  }
  if (!validateCssUrls(decodeCssEscapes(value), safeIds)) {
    return null;
  }
  if (/[\u0000-\u0008\u000b\u000c\u000e-\u001f]/.test(value)) {
    return null;
  }
  return value.trim();
}

function findTopLevelCharacter(value: string, target: string): number {
  let quote = "";
  let depth = 0;
  for (let index = 0; index < value.length; index += 1) {
    const character = value[index];
    if (quote) {
      if (character === "\\") {
        index += 1;
      } else if (character === quote) {
        quote = "";
      }
      continue;
    }
    if (character === '"' || character === "'") {
      quote = character;
      continue;
    }
    if (character === "(") {
      depth += 1;
    } else if (character === ")") {
      depth = Math.max(0, depth - 1);
    } else if (character === target && depth === 0) {
      return index;
    }
  }
  return -1;
}

function splitCssDeclarations(value: string): string[] {
  const declarations: string[] = [];
  let start = 0;
  let quote = "";
  let depth = 0;

  for (let index = 0; index < value.length; index += 1) {
    const character = value[index];
    if (quote) {
      if (character === "\\") {
        index += 1;
      } else if (character === quote) {
        quote = "";
      }
      continue;
    }
    if (character === '"' || character === "'") {
      quote = character;
      continue;
    }
    if (character === "(") {
      depth += 1;
    } else if (character === ")") {
      depth = Math.max(0, depth - 1);
    } else if (character === ";" && depth === 0) {
      declarations.push(value.slice(start, index));
      start = index + 1;
    }
  }
  declarations.push(value.slice(start));
  return declarations;
}

function sanitizeStyleDeclarations(
  value: string,
  safeIds: ReadonlyMap<string, CleanElement>,
): string | null {
  const withoutComments = stripCssComments(value);
  if (withoutComments === null) {
    return null;
  }

  const output: string[] = [];
  for (const rawDeclaration of splitCssDeclarations(withoutComments)) {
    const declaration = rawDeclaration.trim();
    if (!declaration) {
      continue;
    }
    const colon = findTopLevelCharacter(declaration, ":");
    if (colon <= 0) {
      continue;
    }

    const property = declaration.slice(0, colon).trim();
    const propertyValue = declaration.slice(colon + 1).trim();
    const lowerProperty = property.toLowerCase();
    if (
      !/^(?:--[A-Za-z0-9_-]+|[A-Za-z][A-Za-z0-9-]*)$/.test(property) ||
      DANGEROUS_CSS_PROPERTIES.has(lowerProperty) ||
      lowerProperty.startsWith("behavior") ||
      lowerProperty.includes("binding")
    ) {
      continue;
    }

    const safeValue = sanitizeCssValue(propertyValue, safeIds);
    if (safeValue !== null && safeValue.length > 0) {
      output.push(`${property}: ${safeValue}`);
    }
  }

  return output.length > 0 ? output.join("; ") : null;
}

function skipCssString(value: string, index: number): number {
  const quote = value[index];
  index += 1;
  while (index < value.length) {
    if (value[index] === "\\") {
      index += 2;
      continue;
    }
    if (value[index] === quote) {
      return index + 1;
    }
    index += 1;
  }
  return index;
}

function findCssBlockEnd(value: string, openBrace: number): number {
  let depth = 1;
  let index = openBrace + 1;
  while (index < value.length) {
    if (value.startsWith("/*", index)) {
      const end = value.indexOf("*/", index + 2);
      if (end < 0) {
        return -1;
      }
      index = end + 2;
      continue;
    }
    const character = value[index];
    if (character === '"' || character === "'") {
      index = skipCssString(value, index);
      continue;
    }
    if (character === "{") {
      depth += 1;
    } else if (character === "}") {
      depth -= 1;
      if (depth === 0) {
        return index;
      }
    }
    index += 1;
  }
  return -1;
}

function findCssRuleDelimiter(value: string, start: number): {
  brace: number;
  semicolon: number;
} {
  let index = start;
  let brace = -1;
  let semicolon = -1;
  while (index < value.length) {
    if (value.startsWith("/*", index)) {
      const end = value.indexOf("*/", index + 2);
      if (end < 0) {
        return { brace: -1, semicolon: -1 };
      }
      index = end + 2;
      continue;
    }
    const character = value[index];
    if (character === '"' || character === "'") {
      index = skipCssString(value, index);
      continue;
    }
    if (character === "{") {
      brace = index;
      break;
    }
    if (character === ";") {
      semicolon = index;
      break;
    }
    if (character === "}") {
      break;
    }
    index += 1;
  }
  return { brace, semicolon };
}

function sanitizeStylesheet(
  stylesheet: string,
  safeIds: ReadonlyMap<string, CleanElement>,
): string {
  let output = "";
  let index = 0;

  while (index < stylesheet.length) {
    while (
      index < stylesheet.length &&
      /[ \t\r\n\f]/.test(stylesheet[index])
    ) {
      index += 1;
    }
    if (index >= stylesheet.length) {
      break;
    }

    if (stylesheet.startsWith("/*", index)) {
      const end = stylesheet.indexOf("*/", index + 2);
      if (end < 0) {
        break;
      }
      index = end + 2;
      continue;
    }

    const delimiter = findCssRuleDelimiter(stylesheet, index);
    if (delimiter.brace < 0) {
      index =
        delimiter.semicolon >= 0
          ? delimiter.semicolon + 1
          : stylesheet.length;
      continue;
    }

    const selector = stylesheet.slice(index, delimiter.brace).trim();
    const blockEnd = findCssBlockEnd(stylesheet, delimiter.brace);
    if (blockEnd < 0) {
      break;
    }
    const block = stylesheet.slice(delimiter.brace + 1, blockEnd);
    index = blockEnd + 1;

    if (!selector || selector.startsWith("@")) {
      continue;
    }
    if (
      selector.length > MAX_SELECTOR_LENGTH ||
      selector.includes("@") ||
      selector.includes("{") ||
      selector.includes("}") ||
      containsExternalCss(selector)
    ) {
      continue;
    }

    const declarations = sanitizeStyleDeclarations(block, safeIds);
    if (declarations !== null) {
      output += `${selector}{${declarations}}`;
    }
  }

  return output;
}

function sanitizeUrlAttribute(
  value: string,
  safeIds: ReadonlyMap<string, CleanElement>,
): string | null {
  const withoutComments = stripCssComments(value);
  if (withoutComments === null || containsExternalCss(withoutComments)) {
    return null;
  }
  if (!validateCssUrls(decodeCssEscapes(withoutComments), safeIds)) {
    return null;
  }
  return value;
}

function sanitizeHref(
  value: string,
  safeIds: ReadonlyMap<string, CleanElement>,
): string | null {
  const trimmed = value.trim();
  if (!trimmed.startsWith("#") || trimmed.length < 2) {
    return null;
  }
  const id = trimmed.slice(1);
  return isSafeId(id, safeIds) ? `#${id}` : null;
}

function collectAttributes(
  element: XmlElement,
  sanitizer: SvgSanitizer,
): CleanAttribute[] {
  const attributes: CleanAttribute[] = [];
  for (const attribute of element.attributes) {
    const lowerName = attribute.name.toLowerCase();
    if (lowerName.startsWith("on")) {
      sanitizer.recordIssue({
        code: "ATTRIBUTE_REMOVED",
        message: `Removed event handler attribute ${attribute.name}`,
        element: element.localName,
        attribute: attribute.name,
      });
      continue;
    }
    if (
      attribute.name === "xmlns" ||
      attribute.name.startsWith("xmlns:")
    ) {
      continue;
    }

    const isXlinkHref =
      attribute.prefix === "xlink" && attribute.localName === "href";
    if (attribute.prefix && !isXlinkHref) {
      if (
        !(
          attribute.prefix === "xml" &&
          (attribute.localName === "space" || attribute.localName === "lang")
        )
      ) {
        sanitizer.recordIssue({
          code: "ATTRIBUTE_REMOVED",
          message: `Removed attribute ${attribute.name} from an unsupported namespace`,
          element: element.localName,
          attribute: attribute.name,
        });
        continue;
      }
    }

    if (
      !ALLOWED_ATTRIBUTES.has(attribute.localName) &&
      !lowerName.startsWith("data-") &&
      !lowerName.startsWith("aria-")
    ) {
      sanitizer.recordIssue({
        code: "ATTRIBUTE_REMOVED",
        message: `Removed unsupported attribute ${attribute.name}`,
        element: element.localName,
        attribute: attribute.name,
      });
      continue;
    }

    const name =
      isXlinkHref && attribute.prefix === "xlink"
        ? "xlink:href"
        : attribute.name;
    attributes.push({
      name,
      localName: attribute.localName,
      value: attribute.value,
    });
  }
  return attributes;
}

function cleanElement(
  element: XmlElement,
  sanitizer: SvgSanitizer,
  isRoot: boolean,
): CleanElement | null {
  if (!ALLOWED_ELEMENTS.has(element.localName)) {
    sanitizer.recordIssue({
      code: "ELEMENT_REMOVED",
      message: `Removed unsupported element <${element.localName}>`,
      element: element.localName,
    });
    return null;
  }
  if (isRoot && element.localName !== "svg") {
    throw new SvgSanitizationError(
      "INVALID_ROOT",
      "The root element must be <svg>",
    );
  }

  const clean: CleanElement = {
    type: "element",
    name: element.localName,
    attributes: collectAttributes(element, sanitizer),
    children: [],
  };

  if (element.localName === "style") {
    for (const child of element.children) {
      if (child.type === "text") {
        clean.children.push({ type: "text", value: child.value });
      }
    }
    return clean;
  }

  for (const child of element.children) {
    if (child.type === "text") {
      clean.children.push({ type: "text", value: child.value });
      continue;
    }
    const cleanedChild = cleanElement(child, sanitizer, false);
    if (cleanedChild !== null) {
      clean.children.push(cleanedChild);
    }
  }
  return clean;
}

function walkElements(
  node: CleanNode,
  visitor: (element: CleanElement) => void,
): void {
  if (!isElement(node)) {
    return;
  }
  visitor(node);
  for (const child of node.children) {
    walkElements(child, visitor);
  }
}

function collectSafeIds(
  root: CleanElement,
  sanitizer: SvgSanitizer,
): Map<string, CleanElement> {
  const safeIds = new Map<string, CleanElement>();
  walkElements(root, (element) => {
    const idAttribute = getAttribute(element, "id");
    if (!idAttribute) {
      return;
    }
    if (
      idAttribute.value.length === 0 ||
      idAttribute.value.length > MAX_ID_LENGTH ||
      !SAFE_ID.test(idAttribute.value)
    ) {
      sanitizer.recordIssue({
        code: "ATTRIBUTE_REMOVED",
        message: "Removed an invalid element id",
        element: element.name,
        attribute: "id",
      });
      removeAttribute(element, idAttribute);
      return;
    }
    if (safeIds.has(idAttribute.value)) {
      sanitizer.recordIssue({
        code: "ATTRIBUTE_REMOVED",
        message: `Removed duplicate id ${idAttribute.value}`,
        element: element.name,
        attribute: "id",
      });
      removeAttribute(element, idAttribute);
      return;
    }
    safeIds.set(idAttribute.value, element);
  });
  return safeIds;
}

function validateReferences(
  root: CleanElement,
  safeIds: ReadonlyMap<string, CleanElement>,
  sanitizer: SvgSanitizer,
): { edges: ReferenceEdge[]; links: GraphLink[] } {
  const edges: ReferenceEdge[] = [];
  const links: GraphLink[] = [];

  const visit = (element: CleanElement, ownerId?: string): void => {
    const ownId = getAttribute(element, "id")?.value;
    if (ownerId && ownId) {
      links.push({ sourceId: ownerId, targetId: ownId });
    }
    const sourceId = ownId ?? ownerId;
    for (const attribute of [...element.attributes]) {
      if (attribute.localName === "style") {
        const style = sanitizeStyleDeclarations(attribute.value, safeIds);
        if (style === null) {
          removeAttribute(element, attribute);
          sanitizer.recordIssue({
            code: "STYLE_REMOVED",
            message: "Removed an unsafe inline style",
            element: element.name,
            attribute: "style",
          });
        } else {
          attribute.value = style;
        }
        continue;
      }

      if (attribute.localName === "href") {
        const target = sanitizeHref(attribute.value, safeIds);
        if (target === null) {
          removeAttribute(element, attribute);
          sanitizer.recordIssue({
            code: "REFERENCE_REMOVED",
            message: `Removed reference ${attribute.value} outside this document`,
            element: element.name,
            attribute: attribute.name,
          });
        } else {
          attribute.value = target;
          if (sourceId) {
            const edge: ReferenceEdge = {
              element,
              attribute,
              sourceId,
              targetId: target.slice(1),
            };
            edges.push(edge);
            links.push(edge);
          }
        }
        continue;
      }

      if (
        URL_BEARING_ATTRIBUTES.has(attribute.localName) ||
        attribute.value.toLowerCase().includes("url")
      ) {
        const value = sanitizeUrlAttribute(attribute.value, safeIds);
        if (value === null) {
          removeAttribute(element, attribute);
          sanitizer.recordIssue({
            code: "ATTRIBUTE_REMOVED",
            message: `Removed unsafe URL value from ${attribute.name}`,
            element: element.name,
            attribute: attribute.name,
          });
        } else {
          attribute.value = value;
        }
      }
    }

    for (const child of element.children) {
      if (isElement(child)) {
        visit(child, sourceId);
      }
    }
  };
  visit(root);

  return { edges, links };
}

function cyclicComponents(
  nodeIds: ReadonlySet<string>,
  links: readonly GraphLink[],
): Set<string> {
  const graph = new Map<string, Set<string>>();
  for (const id of nodeIds) {
    graph.set(id, new Set());
  }
  for (const link of links) {
    graph.get(link.sourceId)?.add(link.targetId);
  }

  let nextIndex = 0;
  const indexes = new Map<string, number>();
  const lowLinks = new Map<string, number>();
  const stack: string[] = [];
  const onStack = new Set<string>();
  const cyclic = new Set<string>();

  const visit = (node: string): void => {
    indexes.set(node, nextIndex);
    lowLinks.set(node, nextIndex);
    nextIndex += 1;
    stack.push(node);
    onStack.add(node);

    for (const target of graph.get(node) ?? []) {
      if (!indexes.has(target)) {
        visit(target);
        lowLinks.set(
          node,
          Math.min(lowLinks.get(node) ?? 0, lowLinks.get(target) ?? 0),
        );
      } else if (onStack.has(target)) {
        lowLinks.set(
          node,
          Math.min(lowLinks.get(node) ?? 0, indexes.get(target) ?? 0),
        );
      }
    }

    if (lowLinks.get(node) !== indexes.get(node)) {
      return;
    }

    const component: string[] = [];
    let member: string | undefined;
    do {
      member = stack.pop();
      if (member === undefined) {
        break;
      }
      onStack.delete(member);
      component.push(member);
    } while (member !== node);

    const selfLoop = component.length === 1 &&
      (graph.get(component[0])?.has(component[0]) ?? false);
    if (component.length > 1 || selfLoop) {
      for (const componentNode of component) {
        cyclic.add(componentNode);
      }
    }
  };

  for (const node of nodeIds) {
    if (!indexes.has(node)) {
      visit(node);
    }
  }
  return cyclic;
}

function breakReferenceCycles(
  edges: ReferenceEdge[],
  links: GraphLink[],
  safeIds: ReadonlyMap<string, CleanElement>,
  sanitizer: SvgSanitizer,
): void {
  const cyclic = cyclicComponents(new Set(safeIds.keys()), links);
  if (cyclic.size === 0) {
    return;
  }

  for (const edge of edges) {
    if (cyclic.has(edge.sourceId) && cyclic.has(edge.targetId)) {
      removeAttribute(edge.element, edge.attribute);
      sanitizer.recordIssue({
        code: "REFERENCE_REMOVED",
        message: `Removed cyclic reference #${edge.targetId}`,
        element: edge.element.name,
        attribute: edge.attribute.name,
      });
    }
  }
}

function sanitizeStyleElements(
  root: CleanElement,
  safeIds: ReadonlyMap<string, CleanElement>,
  sanitizer: SvgSanitizer,
): void {
  walkElements(root, (element) => {
    if (element.name !== "style") {
      return;
    }
    const source = element.children
      .filter((child): child is CleanText => child.type === "text")
      .map((child) => child.value)
      .join("");
    const sanitized = sanitizeStylesheet(source, safeIds);
    element.children = sanitized
      ? [{ type: "text", value: sanitized }]
      : [];
    if (!sanitized) {
      sanitizer.recordIssue({
        code: "STYLE_REMOVED",
        message: "Removed a stylesheet with no safe rules",
        element: "style",
      });
    }
  });
}

function pruneEmptyStyleElements(node: CleanElement): void {
  node.children = node.children.filter(
    (child) =>
      !(
        isElement(child) &&
        child.name === "style" &&
        child.children.every(
          (styleChild) => styleChild.type === "text" && !styleChild.value.trim(),
        )
      ),
  );
  for (const child of node.children) {
    if (isElement(child)) {
      pruneEmptyStyleElements(child);
    }
  }
}

function escapeXmlText(value: string): string {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

function escapeXmlAttribute(value: string): string {
  return escapeXmlText(value)
    .replace(/"/g, "&quot;")
    .replace(/\r/g, "&#13;")
    .replace(/\n/g, "&#10;")
    .replace(/\t/g, "&#9;");
}

function usesXlink(root: CleanElement): boolean {
  let found = false;
  walkElements(root, (element) => {
    if (element.attributes.some((attribute) => attribute.name === "xlink:href")) {
      found = true;
    }
  });
  return found;
}

function serializeElement(
  element: CleanElement,
  isRoot: boolean,
  addXlinkNamespace: boolean,
): string {
  let output = `<${element.name}`;
  if (isRoot) {
    output += ` xmlns="${SVG_NAMESPACE}"`;
    if (addXlinkNamespace) {
      output += ` xmlns:xlink="${XLINK_NAMESPACE}"`;
    }
  }

  for (const attribute of element.attributes) {
    output += ` ${attribute.name}="${escapeXmlAttribute(attribute.value)}"`;
  }

  if (element.children.length === 0) {
    return `${output}/>`;
  }

  output += ">";
  for (const child of element.children) {
    output += isElement(child)
      ? serializeElement(child, false, false)
      : escapeXmlText(child.value);
  }
  output += `</${element.name}>`;
  return output;
}

export class SvgSanitizer {
  readonly options: Required<SanitizeOptions>;
  private currentIssues: SvgSanitizationIssue[] = [];

  constructor(options: SanitizeOptions = {}) {
    this.options = {
      maxBytes: options.maxBytes ?? DEFAULT_MAX_BYTES,
      maxDepth: options.maxDepth ?? DEFAULT_MAX_DEPTH,
      maxNodes: options.maxNodes ?? DEFAULT_MAX_NODES,
    };
    if (
      !Number.isInteger(this.options.maxBytes) ||
      this.options.maxBytes <= 0 ||
      !Number.isInteger(this.options.maxDepth) ||
      this.options.maxDepth < 1 ||
      !Number.isInteger(this.options.maxNodes) ||
      this.options.maxNodes < 1
    ) {
      throw new SvgSanitizationError(
        "INVALID_OPTIONS",
        "maxBytes, maxDepth, and maxNodes must be positive integers",
      );
    }
  }

  sanitize(input: string): string {
    return this.sanitizeWithReport(input).svg;
  }

  sanitizeWithReport(input: string): SvgSanitizationResult {
    if (typeof input !== "string") {
      throw new SvgSanitizationError(
        "INVALID_INPUT",
        "SVG input must be a string",
      );
    }

    const document = input.charCodeAt(0) === 0xfeff ? input.slice(1) : input;
    const byteLength = new TextEncoder().encode(document).byteLength;
    if (byteLength > this.options.maxBytes) {
      throw new SvgLimitError(
        "MAX_BYTES_EXCEEDED",
        `SVG size ${byteLength} bytes exceeds the limit of ${this.options.maxBytes}`,
      );
    }
    if (document.includes("\u0000")) {
      throw new SvgSanitizationError(
        "INVALID_XML",
        "SVG input contains a NUL character",
      );
    }
    for (let index = 0; index < document.length; index += 1) {
      const codePoint = document.codePointAt(index);
      if (codePoint === undefined || !isValidXmlCodePoint(codePoint)) {
        throw new SvgSanitizationError(
          "INVALID_XML",
          "SVG input contains a character that is not valid XML",
          index,
        );
      }
      if (codePoint > 0xffff) {
        index += 1;
      }
    }

    this.currentIssues = [];
    const parsed = new StrictXmlParser(
      document,
      this.options.maxDepth,
      this.options.maxNodes,
    ).parse();
    if (parsed.localName !== "svg") {
      throw new SvgSanitizationError(
        "INVALID_ROOT",
        "The root element must be <svg>",
      );
    }

    const root = cleanElement(parsed, this, true);
    if (root === null) {
      throw new SvgSanitizationError("INVALID_ROOT", "The SVG root was removed");
    }

    const safeIds = collectSafeIds(root, this);
    const references = validateReferences(root, safeIds, this);
    breakReferenceCycles(references.edges, references.links, safeIds, this);
    sanitizeStyleElements(root, safeIds, this);
    pruneEmptyStyleElements(root);

    return {
      svg: serializeElement(root, true, usesXlink(root)),
      issues: [...this.currentIssues],
    };
  }

  recordIssue(issue: SvgSanitizationIssue): void {
    this.currentIssues.push(issue);
  }
}

export function sanitizeSvg(
  input: string,
  options: SanitizeOptions = {},
): string {
  return new SvgSanitizer(options).sanitize(input);
}

export function sanitizeSvgWithReport(
  input: string,
  options: SanitizeOptions = {},
): SvgSanitizationResult {
  return new SvgSanitizer(options).sanitizeWithReport(input);
}

export const sanitizeVectorImage = sanitizeSvg;
