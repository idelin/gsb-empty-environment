/**
 * A deliberately small, dependency-free SVG sanitizer for untrusted avatar
 * uploads.  The input is parsed as XML, reduced to an allow-list, and
 * serialized again.  Output is intended to be embedded inline in HTML.
 */

export class SvgSanitizationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SvgSanitizationError";
  }
}

interface ParsedAttribute {
  readonly name: string;
  readonly value: string;
}

interface ParsedText {
  readonly type: "text";
  readonly value: string;
}

interface ParsedElement {
  readonly type: "element";
  readonly name: string;
  readonly attributes: readonly ParsedAttribute[];
  readonly children: readonly ParsedNode[];
}

type ParsedNode = ParsedText | ParsedElement;

interface ParsedDocument {
  readonly children: readonly ParsedNode[];
}

const SVG_NAMESPACE = "http://www.w3.org/2000/svg";
const MAX_INPUT_LENGTH = 1_048_576;
const MAX_DEPTH = 100;
const MAX_NODES = 50_000;

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
  "textpath",
  "title",
  "desc",
  "lineargradient",
  "radialgradient",
  "stop",
  "clippath",
  "mask",
  "pattern",
  "marker",
  "filter",
  "feblend",
  "fecolormatrix",
  "fecomponenttransfer",
  "fecomposite",
  "feconvolvematrix",
  "fediffuselighting",
  "fedisplacementmap",
  "fedropshadow",
  "feflood",
  "fefunca",
  "fefuncb",
  "fefuncg",
  "fefuncr",
  "fegaussianblur",
  "femerge",
  "femergenode",
  "femorphology",
  "feoffset",
  "fespecularlighting",
  "fetile",
  "feturbulence",
]);

const ELEMENT_NAME_MAP = new Map<string, string>([
  ["svg", "svg"],
  ["g", "g"],
  ["defs", "defs"],
  ["symbol", "symbol"],
  ["use", "use"],
  ["path", "path"],
  ["rect", "rect"],
  ["circle", "circle"],
  ["ellipse", "ellipse"],
  ["line", "line"],
  ["polyline", "polyline"],
  ["polygon", "polygon"],
  ["text", "text"],
  ["tspan", "tspan"],
  ["textpath", "textPath"],
  ["title", "title"],
  ["desc", "desc"],
  ["lineargradient", "linearGradient"],
  ["radialgradient", "radialGradient"],
  ["stop", "stop"],
  ["clippath", "clipPath"],
  ["mask", "mask"],
  ["pattern", "pattern"],
  ["marker", "marker"],
  ["filter", "filter"],
  ["feblend", "feBlend"],
  ["fecolormatrix", "feColorMatrix"],
  ["fecomponenttransfer", "feComponentTransfer"],
  ["fecomposite", "feComposite"],
  ["feconvolvematrix", "feConvolveMatrix"],
  ["fediffuselighting", "feDiffuseLighting"],
  ["fedisplacementmap", "feDisplacementMap"],
  ["fedropshadow", "feDropShadow"],
  ["feflood", "feFlood"],
  ["fefunca", "feFuncA"],
  ["fefuncb", "feFuncB"],
  ["fefuncg", "feFuncG"],
  ["fefuncr", "feFuncR"],
  ["fegaussianblur", "feGaussianBlur"],
  ["femerge", "feMerge"],
  ["femergenode", "feMergeNode"],
  ["femorphology", "feMorphology"],
  ["feoffset", "feOffset"],
  ["fespecularlighting", "feSpecularLighting"],
  ["fetile", "feTile"],
  ["feturbulence", "feTurbulence"],
]);

const CANONICAL_ATTRIBUTE_NAMES = new Map<string, string>([
  ["id", "id"],
  ["class", "class"],
  ["role", "role"],
  ["focusable", "focusable"],
  ["tabindex", "tabindex"],
  ["lang", "lang"],
  ["xml:lang", "xml:lang"],
  ["xml:space", "xml:space"],
  ["version", "version"],
  ["baseprofile", "baseProfile"],
  ["width", "width"],
  ["height", "height"],
  ["x", "x"],
  ["y", "y"],
  ["x1", "x1"],
  ["y1", "y1"],
  ["x2", "x2"],
  ["y2", "y2"],
  ["cx", "cx"],
  ["cy", "cy"],
  ["r", "r"],
  ["rx", "rx"],
  ["ry", "ry"],
  ["fx", "fx"],
  ["fy", "fy"],
  ["fr", "fr"],
  ["d", "d"],
  ["points", "points"],
  ["pathlength", "pathLength"],
  ["viewbox", "viewBox"],
  ["preserveaspectratio", "preserveAspectRatio"],
  ["zoomandpan", "zoomAndPan"],
  ["transform", "transform"],
  ["transform-origin", "transform-origin"],
  ["gradienttransform", "gradientTransform"],
  ["gradientunits", "gradientUnits"],
  ["spreadmethod", "spreadMethod"],
  ["patternunits", "patternUnits"],
  ["patterncontentunits", "patternContentUnits"],
  ["patterntransform", "patternTransform"],
  ["clippathunits", "clipPathUnits"],
  ["maskunits", "maskUnits"],
  ["maskcontentunits", "maskContentUnits"],
  ["mask-type", "mask-type"],
  ["markerunits", "markerUnits"],
  ["markerwidth", "markerWidth"],
  ["markerheight", "markerHeight"],
  ["markerorient", "markerOrient"],
  ["refx", "refX"],
  ["refy", "refY"],
  ["orient", "orient"],
  ["filterunits", "filterUnits"],
  ["primitiveunits", "primitiveUnits"],
  ["filterres", "filterRes"],
  ["offset", "offset"],
  ["stop-color", "stop-color"],
  ["stop-opacity", "stop-opacity"],
  ["fill", "fill"],
  ["fill-opacity", "fill-opacity"],
  ["fill-rule", "fill-rule"],
  ["stroke", "stroke"],
  ["stroke-opacity", "stroke-opacity"],
  ["stroke-width", "stroke-width"],
  ["stroke-linecap", "stroke-linecap"],
  ["stroke-linejoin", "stroke-linejoin"],
  ["stroke-dasharray", "stroke-dasharray"],
  ["stroke-dashoffset", "stroke-dashoffset"],
  ["stroke-miterlimit", "stroke-miterlimit"],
  ["paint-order", "paint-order"],
  ["vector-effect", "vector-effect"],
  ["opacity", "opacity"],
  ["display", "display"],
  ["visibility", "visibility"],
  ["overflow", "overflow"],
  ["color", "color"],
  ["color-interpolation", "color-interpolation"],
  ["color-interpolation-filters", "color-interpolation-filters"],
  ["color-rendering", "color-rendering"],
  ["shape-rendering", "shape-rendering"],
  ["text-rendering", "text-rendering"],
  ["image-rendering", "image-rendering"],
  ["pointer-events", "pointer-events"],
  ["enable-background", "enable-background"],
  ["mix-blend-mode", "mix-blend-mode"],
  ["isolation", "isolation"],
  ["clip-path", "clip-path"],
  ["clip-rule", "clip-rule"],
  ["mask", "mask"],
  ["filter", "filter"],
  ["marker-start", "marker-start"],
  ["marker-mid", "marker-mid"],
  ["marker-end", "marker-end"],
  ["font-family", "font-family"],
  ["font-size", "font-size"],
  ["font-style", "font-style"],
  ["font-weight", "font-weight"],
  ["text-anchor", "text-anchor"],
  ["dominant-baseline", "dominant-baseline"],
  ["alignment-baseline", "alignment-baseline"],
  ["letter-spacing", "letter-spacing"],
  ["word-spacing", "word-spacing"],
  ["baseline-shift", "baseline-shift"],
  ["direction", "direction"],
  ["unicode-bidi", "unicode-bidi"],
  ["writing-mode", "writing-mode"],
  ["textlength", "textLength"],
  ["lengthadjust", "lengthAdjust"],
  ["startoffset", "startOffset"],
  ["method", "method"],
  ["spacing", "spacing"],
  ["rotate", "rotate"],
  ["side", "side"],
  ["style", "style"],
  ["href", "href"],
  ["in", "in"],
  ["in2", "in2"],
  ["result", "result"],
  ["stddeviation", "stdDeviation"],
  ["dx", "dx"],
  ["dy", "dy"],
  ["flood-color", "flood-color"],
  ["flood-opacity", "flood-opacity"],
  ["mode", "mode"],
  ["operator", "operator"],
  ["k1", "k1"],
  ["k2", "k2"],
  ["k3", "k3"],
  ["k4", "k4"],
  ["values", "values"],
  ["type", "type"],
  ["tablevalues", "tableValues"],
  ["slope", "slope"],
  ["intercept", "intercept"],
  ["amplitude", "amplitude"],
  ["exponent", "exponent"],
  ["radius", "radius"],
  ["basefrequency", "baseFrequency"],
  ["numoctaves", "numOctaves"],
  ["seed", "seed"],
  ["stitchtiles", "stitchTiles"],
  ["scale", "scale"],
  ["xchannelselector", "xChannelSelector"],
  ["ychannelselector", "yChannelSelector"],
  ["surfacescale", "surfaceScale"],
  ["diffuseconstant", "diffuseConstant"],
  ["specularconstant", "specularConstant"],
  ["specularexponent", "specularExponent"],
  ["kernelmatrix", "kernelMatrix"],
  ["kernelunitlength", "kernelUnitLength"],
  ["order", "order"],
  ["divisor", "divisor"],
  ["bias", "bias"],
  ["targetx", "targetX"],
  ["targety", "targetY"],
  ["preservealpha", "preserveAlpha"],
  ["edgemode", "edgeMode"],
]);

const URI_PRESENTATION_ATTRIBUTES = new Set([
  "clip-path",
  "mask",
  "filter",
  "marker-start",
  "marker-mid",
  "marker-end",
]);

const PAINT_ATTRIBUTES = new Set([
  "fill",
  "stroke",
  "stop-color",
  "flood-color",
  "lighting-color",
  "color",
]);

const HREF_ELEMENTS = new Set([
  "use",
  "lineargradient",
  "radialgradient",
  "pattern",
  "filter",
  "textpath",
]);

const TEXT_CONTAINING_ELEMENTS = new Set(["text", "tspan", "textpath", "title", "desc"]);

const ALLOWED_STYLE_PROPERTIES = new Set([
  "alignment-baseline",
  "baseline-shift",
  "clip-path",
  "clip-rule",
  "color",
  "color-rendering",
  "color-interpolation",
  "color-interpolation-filters",
  "direction",
  "display",
  "dominant-baseline",
  "enable-background",
  "fill",
  "fill-opacity",
  "fill-rule",
  "filter",
  "flood-color",
  "flood-opacity",
  "font-family",
  "font-size",
  "font-style",
  "font-weight",
  "image-rendering",
  "isolation",
  "letter-spacing",
  "lighting-color",
  "marker-end",
  "marker-mid",
  "marker-start",
  "mask",
  "mask-type",
  "mix-blend-mode",
  "opacity",
  "overflow",
  "paint-order",
  "pointer-events",
  "shape-rendering",
  "stop-color",
  "stop-opacity",
  "stroke",
  "stroke-dasharray",
  "stroke-dashoffset",
  "stroke-linecap",
  "stroke-linejoin",
  "stroke-miterlimit",
  "stroke-opacity",
  "stroke-width",
  "text-anchor",
  "text-rendering",
  "transform",
  "transform-box",
  "transform-origin",
  "unicode-bidi",
  "vector-effect",
  "visibility",
  "word-spacing",
  "writing-mode",
]);

const NAMED_ENTITIES = new Map<string, string>([
  ["amp", "&"],
  ["lt", "<"],
  ["gt", ">"],
  ["quot", '"'],
  ["apos", "'"],
  ["nbsp", "\u00a0"],
  ["colon", ":"],
  ["tab", "\t"],
  ["Tab", "\t"],
  ["newline", "\n"],
  ["NewLine", "\n"],
  ["sol", "/"],
  ["bsol", "\\"],
  ["num", "#"],
  ["quest", "?"],
  ["lpar", "("],
  ["rpar", ")"],
]);

interface TagName {
  readonly prefix: string | undefined;
  readonly local: string;
}

class XmlParser {
  private readonly source: string;
  private index = 0;
  private nodeCount = 0;

  constructor(source: string) {
    this.source = source;
  }

  parse(): ParsedDocument {
    const children = this.parseChildren(undefined, 0);
    this.skipWhitespace();
    if (this.index !== this.source.length) {
      throw new SvgSanitizationError("Unexpected content after the SVG document");
    }
    return { children };
  }

  private parseChildren(
    parentName: string | undefined,
    depth: number,
  ): ParsedNode[] {
    if (depth > MAX_DEPTH) {
      throw new SvgSanitizationError("SVG nesting is too deep");
    }

    const children: ParsedNode[] = [];
    while (this.index < this.source.length) {
      if (this.source.startsWith("<!--", this.index)) {
        this.skipComment();
        continue;
      }
      if (this.source.startsWith("<![CDATA[", this.index)) {
        const value = this.readCdata();
        children.push({ type: "text", value });
        this.countNode();
        continue;
      }
      if (this.source.startsWith("<?", this.index)) {
        this.skipProcessingInstruction();
        continue;
      }
      if (this.source.startsWith("<!", this.index)) {
        this.skipDeclaration();
        continue;
      }
      if (this.source.startsWith("</", this.index)) {
        if (parentName === undefined) {
          throw new SvgSanitizationError("Unexpected closing tag");
        }
        const closingName = this.readClosingTag();
        if (closingName.local.toLowerCase() !== parentName.toLowerCase()) {
          throw new SvgSanitizationError(
            `Mismatched closing tag </${closingName.local}>`,
          );
        }
        return children;
      }
      if (this.source[this.index] === "<") {
        const element = this.readElement(depth);
        children.push(element);
        this.countNode();
        continue;
      }

      const text = this.readText();
      if (text.length > 0) {
        children.push({ type: "text", value: text });
        this.countNode();
      } else if (this.index < this.source.length && this.source[this.index] === "<") {
        throw new SvgSanitizationError("Invalid XML content");
      }
    }

    // Browser HTML parsing implicitly closes open elements at EOF.  Returning
    // the partial tree is safe because it is still rebuilt from the allow-list.
    return children;
  }

  private readElement(depth: number): ParsedElement {
    const start = this.index;
    this.index += 1;
    const name = this.readTagName();
    if (name.local.length === 0) {
      throw new SvgSanitizationError("Empty element name");
    }
    this.skipWhitespace();

    const attributes = this.readAttributes();
    const selfClosing = this.source[this.index] === "/";
    if (selfClosing) {
      this.index += 1;
      this.skipWhitespace();
    }
    if (this.index >= this.source.length) {
      // Tolerate a truncated final tag just as HTML parsing does at EOF.
    } else if (this.source[this.index] !== ">") {
      throw new SvgSanitizationError("Unterminated opening tag");
    } else {
      this.index += 1;
    }

    const localName = name.local.toLowerCase();
    if (
      !selfClosing &&
      (localName === "script" ||
        localName === "style" ||
        localName === "foreignobject" ||
        !ALLOWED_ELEMENTS.has(localName))
    ) {
      const rawText = this.readRawText(
        localName,
        localName !== "script" && localName !== "style",
      );
      return {
        type: "element",
        name: name.local,
        attributes,
        children: rawText.length > 0 ? [{ type: "text", value: rawText }] : [],
      };
    }

    const children = selfClosing
      ? []
      : this.parseChildren(name.local, depth + 1);

    if (this.index === start) {
      throw new SvgSanitizationError("Parser made no progress");
    }

    return {
      type: "element",
      name: name.local,
      attributes,
      children,
    };
  }

  private readAttributes(): ParsedAttribute[] {
    const attributes: ParsedAttribute[] = [];
    while (this.index < this.source.length) {
      this.skipWhitespace();
      if (this.index >= this.source.length) break;

      if (this.source[this.index] === ">" || this.source.startsWith("/>", this.index)) {
        break;
      }
      if (this.source[this.index] === "/") {
        this.index += 1;
        continue;
      }

      const name = this.readAttributeName();
      this.skipWhitespace();

      let value = "";
      if (this.source[this.index] === "=") {
        this.index += 1;
        this.skipWhitespace();
        value = this.readAttributeValue();
      }

      attributes.push({
        name,
        value: decodeEntitiesOnce(value),
      });
    }
    return attributes;
  }

  private readAttributeName(): string {
    const start = this.index;
    while (this.index < this.source.length) {
      const char = this.source[this.index]!;
      if (
        char === "=" ||
        char === ">" ||
        char === "/" ||
        char === "<" ||
        char === '"' ||
        char === "'" ||
        isXmlWhitespace(char)
      ) {
        break;
      }
      this.index += 1;
    }
    if (this.index === start) {
      throw new SvgSanitizationError("Invalid attribute name");
    }
    return this.source.slice(start, this.index);
  }

  private readAttributeValue(): string {
    const quote = this.source[this.index];
    if (quote === '"' || quote === "'") {
      this.index += 1;
      const start = this.index;
      while (this.index < this.source.length) {
        if (this.source[this.index] === quote) {
          const value = this.source.slice(start, this.index);
          this.index += 1;
          return value;
        }
        this.index += 1;
      }
      throw new SvgSanitizationError("Unterminated quoted attribute value");
    }

    const start = this.index;
    while (this.index < this.source.length) {
      const char = this.source[this.index]!;
      if (
        isXmlWhitespace(char) ||
        char === ">" ||
        (char === "/" && this.source[this.index + 1] === ">")
      ) {
        break;
      }
      if (char === "<" || char === '"' || char === "'") {
        throw new SvgSanitizationError("Invalid unquoted attribute value");
      }
      this.index += 1;
    }
    return this.source.slice(start, this.index);
  }

  private readTagName(): TagName {
    const start = this.index;
    while (this.index < this.source.length) {
      const char = this.source[this.index]!;
      if (isXmlWhitespace(char) || char === ">" || char === "/") break;
      if (char === "<" || char === "=" || char === '"' || char === "'") {
        throw new SvgSanitizationError("Invalid element name");
      }
      this.index += 1;
    }
    return splitQName(this.source.slice(start, this.index));
  }

  private readClosingTag(): TagName {
    this.index += 2;
    this.skipWhitespace();
    const name = this.readTagName();
    this.skipWhitespace();
    if (this.index >= this.source.length) {
      return name;
    }
    if (this.source[this.index] !== ">") {
      throw new SvgSanitizationError("Unterminated closing tag");
    }
    this.index += 1;
    return name;
  }

  private readText(): string {
    const start = this.index;
    while (this.index < this.source.length && this.source[this.index] !== "<") {
      this.index += 1;
    }
    return decodeEntitiesOnce(this.source.slice(start, this.index));
  }

  private readCdata(): string {
    const start = this.index + "<![CDATA[".length;
    const end = this.source.indexOf("]]>", start);
    if (end === -1) {
      throw new SvgSanitizationError("Unterminated CDATA section");
    }
    this.index = end + 3;
    return this.source.slice(start, end);
  }

  private readRawText(localName: string, trackNesting: boolean): string {
    const start = this.index;
    let search = start;
    let depth = 1;
    while (true) {
      const nextTag = this.source.indexOf("<", search);
      if (nextTag === -1) {
        this.index = this.source.length;
        return this.source.slice(start);
      }

      if (this.source.startsWith("<!--", nextTag)) {
        const end = this.source.indexOf("-->", nextTag + 4);
        if (end === -1) {
          throw new SvgSanitizationError("Unterminated XML comment");
        }
        search = end + 3;
        continue;
      }
      if (this.source.startsWith("<![CDATA[", nextTag)) {
        const end = this.source.indexOf("]]>", nextTag + 9);
        if (end === -1) {
          throw new SvgSanitizationError("Unterminated CDATA section");
        }
        search = end + 3;
        continue;
      }
      if (this.source.startsWith("<?", nextTag)) {
        const end = this.source.indexOf("?>", nextTag + 2);
        if (end === -1) {
          throw new SvgSanitizationError("Unterminated processing instruction");
        }
        search = end + 2;
        continue;
      }

      const isClosing = this.source.startsWith("</", nextTag);
      let cursor = nextTag + (isClosing ? 2 : 1);
      while (cursor < this.source.length && isXmlWhitespace(this.source[cursor]!)) {
        cursor += 1;
      }
      const nameStart = cursor;
      while (cursor < this.source.length) {
        const char = this.source[cursor]!;
        if (isXmlWhitespace(char) || char === ">" || char === "/") break;
        cursor += 1;
      }
      const candidate = splitQName(this.source.slice(nameStart, cursor));
      if (candidate.local.toLowerCase() === localName) {
        if (isClosing) {
          depth -= 1;
          if (depth === 0) {
            this.index = nextTag;
            const parsedName = this.readClosingTag();
            if (parsedName.local.toLowerCase() !== localName) {
              throw new SvgSanitizationError(
                `Mismatched closing tag </${parsedName.local}>`,
              );
            }
            return this.source.slice(start, nextTag);
          }
        } else if (trackNesting) {
          if (!this.isSelfClosingTagAt(nextTag)) {
            depth += 1;
          }
        }
      }
      search = Math.max(cursor, nextTag + 1);
    }
  }

  private isSelfClosingTagAt(start: number): boolean {
    let cursor = start + 1;
    let quote: string | undefined;
    let lastNonWhitespace = "";
    while (cursor < this.source.length) {
      const char = this.source[cursor]!;
      if (quote !== undefined) {
        if (char === quote) quote = undefined;
      } else if (char === '"' || char === "'") {
        quote = char;
      } else if (char === ">") {
        return lastNonWhitespace === "/";
      } else if (!isXmlWhitespace(char)) {
        lastNonWhitespace = char;
      }
      cursor += 1;
    }
    return false;
  }

  private skipWhitespace(): void {
    while (
      this.index < this.source.length &&
      isXmlWhitespace(this.source[this.index]!)
    ) {
      this.index += 1;
    }
  }

  private skipComment(): void {
    const end = this.source.indexOf("-->", this.index + 4);
    if (end === -1) {
      throw new SvgSanitizationError("Unterminated XML comment");
    }
    this.index = end + 3;
  }

  private skipProcessingInstruction(): void {
    const end = this.source.indexOf("?>", this.index + 2);
    if (end === -1) {
      throw new SvgSanitizationError("Unterminated processing instruction");
    }
    this.index = end + 2;
  }

  private skipDeclaration(): void {
    let bracketDepth = 0;
    let quote: string | undefined;
    let cursor = this.index + 2;
    while (cursor < this.source.length) {
      const char = this.source[cursor]!;
      if (quote !== undefined) {
        if (char === quote) quote = undefined;
      } else if (char === '"' || char === "'") {
        quote = char;
      } else if (char === "[") {
        bracketDepth += 1;
      } else if (char === "]") {
        bracketDepth = Math.max(0, bracketDepth - 1);
      } else if (char === ">" && bracketDepth === 0) {
        this.index = cursor + 1;
        return;
      }
      cursor += 1;
    }
    throw new SvgSanitizationError("Unterminated declaration");
  }

  private countNode(): void {
    this.nodeCount += 1;
    if (this.nodeCount > MAX_NODES) {
      throw new SvgSanitizationError("SVG contains too many nodes");
    }
  }
}

function splitQName(name: string): TagName {
  const colon = name.indexOf(":");
  if (colon === -1) {
    return { prefix: undefined, local: name };
  }
  return {
    prefix: name.slice(0, colon),
    local: name.slice(colon + 1),
  };
}

function isXmlWhitespace(char: string): boolean {
  return char === " " || char === "\t" || char === "\n" || char === "\r";
}

function decodeEntitiesOnce(value: string): string {
  return value.replace(
    /&(#x[0-9a-fA-F]+;|#X[0-9a-fA-F]+;|#[0-9]+;|[a-zA-Z][a-zA-Z0-9]+;)/g,
    (match, body: string) => {
      const decoded = decodeEntityBody(body);
      return decoded === undefined ? match : decoded;
    },
  );
}

function decodeEntitiesDeep(value: string): string {
  let current = value;
  for (let pass = 0; pass < 10; pass += 1) {
    const next = decodeEntitiesOnce(current);
    if (next === current) break;
    current = next;
  }
  return current;
}

function decodeEntityBody(body: string): string | undefined {
  if (
    (body.startsWith("#x") || body.startsWith("#X")) &&
    body.endsWith(";")
  ) {
    return decodeCodePoint(body.slice(2, -1), 16);
  }
  if (body.startsWith("#") && body.endsWith(";")) {
    return decodeCodePoint(body.slice(1, -1), 10);
  }
  if (body.endsWith(";")) {
    return NAMED_ENTITIES.get(body.slice(0, -1));
  }
  return undefined;
}

function decodeCodePoint(raw: string, radix: 16 | 10): string | undefined {
  if (raw.length === 0) return undefined;
  const codePoint = Number.parseInt(raw, radix);
  if (!Number.isFinite(codePoint) || codePoint <= 0 || codePoint > 0x10ffff) {
    return "\ufffd";
  }
  if (codePoint >= 0xd800 && codePoint <= 0xdfff) {
    return "\ufffd";
  }
  return String.fromCodePoint(codePoint);
}

function sanitizeNode(
  node: ParsedNode,
  parentLocalName: string | undefined,
  isRoot: boolean,
): string {
  if (node.type === "text") {
    if (
      parentLocalName === undefined ||
      !TEXT_CONTAINING_ELEMENTS.has(parentLocalName)
    ) {
      return "";
    }
    return escapeXml(removeInvalidXmlCharacters(node.value));
  }

  const localName = splitQName(node.name).local.toLowerCase();
  if (!ALLOWED_ELEMENTS.has(localName)) {
    return "";
  }

  const canonicalName = ELEMENT_NAME_MAP.get(localName)!;
  const rootName = isRoot ? "svg" : canonicalName;
  if (isRoot && localName !== "svg") {
    return "";
  }

  const attributes = sanitizeAttributes(localName, node.attributes);
  const attributeText = attributes
    .map(([name, value]) => ` ${name}="${escapeXml(removeInvalidXmlCharacters(value))}"`)
    .join("");
  const namespace = isRoot ? ` xmlns="${SVG_NAMESPACE}"` : "";

  const children = node.children
    .map((child) => sanitizeNode(child, localName, false))
    .join("");

  if (children.length === 0) {
    return `<${rootName}${namespace}${attributeText}/>`;
  }
  return `<${rootName}${namespace}${attributeText}>${children}</${rootName}>`;
}

function sanitizeAttributes(
  elementLocalName: string,
  attributes: readonly ParsedAttribute[],
): Array<[string, string]> {
  const result: Array<[string, string]> = [];
  const seen = new Set<string>();

  for (const attribute of attributes) {
    const qualified = splitQName(attribute.name);
    const prefix = qualified.prefix?.toLowerCase();
    const local = qualified.local.toLowerCase();

    if (prefix === "xmlns" || local === "xmlns") continue;
    if (local.startsWith("on")) continue;

    let canonical: string | undefined;
    if (local.startsWith("aria-")) {
      canonical = local;
    } else if (
      !qualified.prefix &&
      local === "lang"
    ) {
      canonical = "lang";
    } else if (
      qualified.prefix?.toLowerCase() === "xml" &&
      (local === "lang" || local === "space")
    ) {
      canonical = `xml:${local}`;
    } else {
      canonical = CANONICAL_ATTRIBUTE_NAMES.get(local);
    }

    if (canonical === undefined || seen.has(canonical)) continue;

    let value = attribute.value.trim();

    if (local === "href") {
      if (!HREF_ELEMENTS.has(elementLocalName)) continue;
      const href = sanitizeHref(value);
      if (href === null) continue;
      value = href;
      canonical = "href";
    } else if (local === "style") {
      const style = sanitizeStyle(value);
      if (style === null) continue;
      value = style;
    } else if (PAINT_ATTRIBUTES.has(local)) {
      const paint = sanitizePaintValue(value);
      if (paint === null) continue;
      value = paint;
    } else if (URI_PRESENTATION_ATTRIBUTES.has(local)) {
      const uri = sanitizeLocalUrlValue(value);
      if (uri === null) continue;
      value = uri;
    }

    if (elementLocalName === "svg" && local === "href") {
      continue;
    }

    seen.add(canonical);
    result.push([canonical, value]);
  }

  return result;
}

function sanitizeHref(value: string): string | null {
  const decoded = decodeEntitiesDeep(value);
  const normalized = removeProtocolWhitespace(decoded);
  if (!normalized.startsWith("#") || normalized.length === 1) {
    return null;
  }
  if (!isSafeFragment(normalized)) {
    return null;
  }
  return normalized;
}

function sanitizeLocalUrlValue(value: string): string | null {
  const normalized = prepareCssForChecks(value);
  if (/^none$/i.test(normalized)) return "none";
  if (!hasUrlFunction(normalized) || containsUnsafeCssToken(normalized)) {
    return null;
  }

  const result = normalizeLocalCssUrls(normalized);
  if (result === null) return null;
  return result;
}

function sanitizePaintValue(value: string): string | null {
  const normalized = prepareCssForChecks(value);
  if (normalized.length === 0) return null;

  if (containsAtRule(normalized)) return null;
  if (containsUnsafeCssToken(normalized)) return null;
  if (containsNakedCssScheme(normalized)) return null;

  if (hasUrlFunction(normalized)) {
    const result = normalizeLocalCssUrls(normalized);
    if (result === null) return null;
    return result;
  }
  return normalized;
}

function sanitizeStyle(value: string): string | null {
  const normalized = prepareCssForChecks(value);
  if (normalized.length === 0) return null;

  const declarations = splitCssDeclarations(normalized);
  const safeDeclarations: string[] = [];

  for (const declaration of declarations) {
    if (
      containsAtRule(declaration) ||
      hasUrlFunction(declaration) ||
      containsUnsafeCssToken(declaration)
    ) {
      continue;
    }
    const colon = declaration.indexOf(":");
    if (colon <= 0) continue;
    const property = declaration.slice(0, colon).trim().toLowerCase();
    const propertyValue = declaration.slice(colon + 1).trim();
    if (!ALLOWED_STYLE_PROPERTIES.has(property) || propertyValue.length === 0) {
      continue;
    }
    if (containsNakedCssScheme(propertyValue)) continue;
    safeDeclarations.push(`${property}:${propertyValue}`);
  }

  return safeDeclarations.length > 0 ? safeDeclarations.join(";") : null;
}

function splitCssDeclarations(value: string): string[] {
  const declarations: string[] = [];
  let start = 0;
  let quote: string | undefined;
  let parenDepth = 0;
  let escaped = false;

  for (let index = 0; index < value.length; index += 1) {
    const char = value[index]!;
    if (escaped) {
      escaped = false;
      continue;
    }
    if (char === "\\") {
      escaped = true;
      continue;
    }
    if (quote !== undefined) {
      if (char === quote) quote = undefined;
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      continue;
    }
    if (char === "(") {
      parenDepth += 1;
      continue;
    }
    if (char === ")") {
      parenDepth = Math.max(0, parenDepth - 1);
      continue;
    }
    if (char === ";" && parenDepth === 0) {
      declarations.push(value.slice(start, index));
      start = index + 1;
    }
  }
  declarations.push(value.slice(start));
  return declarations;
}

function stripCssComments(value: string): string {
  let result = "";
  let cursor = 0;
  while (cursor < value.length) {
    const start = value.indexOf("/*", cursor);
    if (start === -1) {
      result += value.slice(cursor);
      break;
    }
    result += value.slice(cursor, start);
    const end = value.indexOf("*/", start + 2);
    if (end === -1) break;
    cursor = end + 2;
  }
  return result;
}

function prepareCssForChecks(value: string): string {
  let current = value;
  for (let pass = 0; pass < 5; pass += 1) {
    const next = removeInvalidXmlCharacters(
      decodeCssEscapes(stripCssComments(decodeEntitiesDeep(current))),
    );
    if (next === current) break;
    current = next;
  }
  return current.trim();
}

function decodeCssEscapes(value: string): string {
  let current = value.replace(/\\(?:\r\n|\n|\r)/g, "");
  for (let pass = 0; pass < 16; pass += 1) {
    const next = current.replace(
      /\\(?:([0-9a-fA-F]{1,6})(?:[ \t\n\r\f])?|(.))/g,
      (_match, hex: string | undefined, char: string | undefined) => {
        if (hex !== undefined) {
          const codePoint = Number.parseInt(hex, 16);
          if (
            codePoint === 0 ||
            codePoint > 0x10ffff ||
            (codePoint >= 0xd800 && codePoint <= 0xdfff)
          ) {
            return "\ufffd";
          }
          return String.fromCodePoint(codePoint);
        }
        return char ?? "";
      },
    );
    if (next === current) break;
    current = next;
  }
  return current;
}

function containsAtRule(value: string): boolean {
  return /@/.test(value) || /@\s*i\s*m\s*p\s*o\s*r\s*t/i.test(value);
}

function containsUnsafeCssToken(value: string): boolean {
  return /(?:^|[^a-z0-9_-])(?:expression|javascript|vbscript|livescript|mocha|data|https?|ftp|file|progid)\s*[:(]/i.test(
    value,
  ) ||
    /(?:-moz-binding|behavior|image|image-set|cross-fade|element|paint|src|canvas|var|env|attr)\s*\(/i.test(
      value,
    );
}

function containsNakedCssScheme(value: string): boolean {
  return /(?:^|[\s,;()"'])[a-z][a-z0-9+.-]*:/i.test(value);
}

function hasUrlFunction(value: string): boolean {
  return /(?:^|[^a-z0-9_-])u\s*r\s*l\s*\(/i.test(value);
}

function normalizeLocalCssUrls(value: string): string | null {
  const urlFunction = /url\(\s*(?:(["'])(.*?)\1|([^)"']*?))\s*\)/gi;
  const remainder = value.replace(urlFunction, "");
  let invalid = false;
  const result = value.replace(
    urlFunction,
    (_match, _quote: string | undefined, quoted: string | undefined, bare: string | undefined) => {
      const target = decodeEntitiesDeep((quoted ?? bare ?? "").trim());
      const normalized = removeProtocolWhitespace(target);
      if (!normalized.startsWith("#") || !isSafeFragment(normalized)) {
        invalid = true;
        return "";
      }
      return `url(${normalized})`;
    },
  );
  if (invalid || hasUrlFunction(remainder)) return null;
  return result;
}

function isSafeFragment(value: string): boolean {
  return (
    value.startsWith("#") &&
    !/[\u0000-\u0020\u007f-\u009f"<>]/.test(value.slice(1))
  );
}

function removeProtocolWhitespace(value: string): string {
  return value
    .replace(/[\u0000-\u0020\u007f-\u009f\u00a0\u1680\u2000-\u200f\u2028-\u202f\u205f\u3000\ufeff]/g, "");
}

function removeInvalidXmlCharacters(value: string): string {
  let result = "";
  for (let index = 0; index < value.length; index += 1) {
    const codeUnit = value.charCodeAt(index);
    if (codeUnit >= 0xd800 && codeUnit <= 0xdbff) {
      const next = value.charCodeAt(index + 1);
      if (next >= 0xdc00 && next <= 0xdfff) {
        result += value[index]! + value[index + 1]!;
        index += 1;
      }
      continue;
    }
    if (codeUnit >= 0xdc00 && codeUnit <= 0xdfff) continue;
    if (
      codeUnit === 0x9 ||
      codeUnit === 0xa ||
      codeUnit === 0xd ||
      (codeUnit >= 0x20 && codeUnit <= 0xd7ff) ||
      (codeUnit >= 0xe000 && codeUnit <= 0xfffd)
    ) {
      result += value[index]!;
    }
  }
  return result;
}

function escapeXml(value: string): string {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

/**
 * Sanitize an uploaded SVG document into a conservative inline-safe icon.
 *
 * @throws SvgSanitizationError when the input is malformed, too large, or
 * does not have a single <svg> root element.
 */
export function sanitizeSvg(input: string): string {
  if (typeof input !== "string") {
    throw new SvgSanitizationError("SVG input must be a string");
  }
  if (input.length > MAX_INPUT_LENGTH) {
    throw new SvgSanitizationError("SVG input is too large");
  }

  const document = new XmlParser(input).parse();
  const meaningful = document.children.filter((node) => {
    if (node.type === "element") return true;
    return node.value.trim().length > 0;
  });

  const first = meaningful[0];
  if (meaningful.length !== 1 || first === undefined || first.type !== "element") {
    throw new SvgSanitizationError("SVG must have exactly one root element");
  }

  const root = first;
  if (splitQName(root.name).local.toLowerCase() !== "svg") {
    throw new SvgSanitizationError("The root element must be <svg>");
  }

  return sanitizeNode(root, undefined, true);
}

export const sanitizeSVG = sanitizeSvg;
export default sanitizeSvg;
