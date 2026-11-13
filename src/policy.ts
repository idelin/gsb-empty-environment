import type { Namespace } from "./types.ts";

export const HTML_ELEMENTS = new Set([
  "a",
  "abbr",
  "b",
  "blockquote",
  "br",
  "cite",
  "code",
  "dd",
  "div",
  "dl",
  "dt",
  "em",
  "figcaption",
  "figure",
  "h1",
  "h2",
  "h3",
  "h4",
  "h5",
  "h6",
  "hr",
  "i",
  "img",
  "li",
  "nl",
  "ol",
  "p",
  "pre",
  "s",
  "span",
  "strong",
  "sub",
  "sup",
  "table",
  "tbody",
  "td",
  "th",
  "thead",
  "tr",
  "u",
  "ul",
]);

export const SVG_ELEMENTS = new Set([
  "svg",
  "g",
  "defs",
  "desc",
  "title",
  "lineargradient",
  "radialgradient",
  "stop",
  "path",
  "rect",
  "circle",
  "ellipse",
  "line",
  "polyline",
  "polygon",
  "text",
  "tspan",
  "foreignobject",
]);

const GLOBAL_HTML_ATTRIBUTES = new Set([
  "class",
  "dir",
  "id",
  "lang",
  "title",
  "xml:lang",
]);

const HTML_ATTRIBUTE_ALLOWLIST: Record<string, Set<string>> = {
  a: new Set(["href", "hreflang", "rel", "target"]),
  blockquote: new Set(["cite"]),
  img: new Set(["alt", "height", "src", "width"]),
  q: new Set(["cite"]),
};

const GLOBAL_SVG_ATTRIBUTES = new Set([
  "class",
  "id",
  "xml:lang",
  "viewbox",
  "width",
  "height",
  "x",
  "y",
  "cx",
  "cy",
  "r",
  "rx",
  "ry",
  "x1",
  "x2",
  "y1",
  "y2",
  "d",
  "fill",
  "fill-opacity",
  "stroke",
  "stroke-width",
  "stroke-opacity",
  "opacity",
  "transform",
  "offset",
  "stop-color",
  "stop-opacity",
  "gradientunits",
  "gradienttransform",
  "points",
  "text-anchor",
  "font-size",
  "font-family",
  "font-weight",
  "preserveaspectratio",
  "xmlns",
]);

const SVG_ATTRIBUTE_ALLOWLIST: Record<string, Set<string>> = {
  a: new Set(["href", "xlink:href"]),
  use: new Set(["href", "xlink:href"]),
  image: new Set(["href", "xlink:href", "preserveaspectratio"]),
};

// URL-bearing attributes whose value must pass the scheme check.
export const URL_ATTRIBUTES = new Set([
  "href",
  "src",
  "cite",
  "xlink:href",
  "action",
  "formaction",
  "background",
  "poster",
  "data",
]);

const ALLOWED_URL_SCHEMES = new Set(["http:", "https:", "mailto:", "tel:"]);

export function isEventAttribute(name: string): boolean {
  return name.length > 2 && name.startsWith("on");
}

export function isElementAllowed(
  tag: string,
  namespace: Namespace,
  inForeign: boolean,
): boolean {
  // The SVG wrapper that hosts the embedded HTML document.
  if (tag === "foreignobject" && namespace === "svg") return true;
  // Content inside foreignObject is parsed and filtered as HTML.
  if (inForeign) return HTML_ELEMENTS.has(tag);
  return namespace === "svg"
    ? SVG_ELEMENTS.has(tag)
    : HTML_ELEMENTS.has(tag);
}

export function isAttributeAllowed(
  tag: string,
  name: string,
  namespace: Namespace,
  inForeign: boolean,
): boolean {
  // The foreignObject wrapper itself is still an SVG element, so it keeps
  // SVG geometry/presentation attributes.
  const isForeignWrapper = tag === "foreignobject" && namespace === "svg";
  // Everything nested inside foreignObject follows the HTML policy.
  if ((namespace === "html" || inForeign) && !isForeignWrapper) {
    if (GLOBAL_HTML_ATTRIBUTES.has(name)) return true;
    return HTML_ATTRIBUTE_ALLOWLIST[tag]?.has(name) ?? false;
  }
  if (GLOBAL_SVG_ATTRIBUTES.has(name)) return true;
  return SVG_ATTRIBUTE_ALLOWLIST[tag]?.has(name) ?? false;
}

function stripControlChars(value: string): string {
  let result = "";
  for (const char of value) {
    const code = char.charCodeAt(0);
    if (code > 0x1f && code !== 0x7f) result += char;
  }
  return result;
}

// Rejects javascript:/vbscript:/data: URLs and control-char obfuscation.
export function isSafeUrl(rawValue: string): boolean {
  const value = stripControlChars(rawValue).trim();
  if (value === "") return false;

  const colon = value.indexOf(":");
  if (colon === -1) {
    // Relative URL or anchor.
    return true;
  }

  const scheme = value.slice(0, colon).toLowerCase();
  if (scheme === "" || /[^a-z0-9+\-.]/.test(scheme)) {
    // Not a real scheme (e.g. port-like or odd fragments) — treat as relative.
    return true;
  }
  return ALLOWED_URL_SCHEMES.has(scheme + ":");
}

// Lower-cased SVG attribute name -> canonical spelling used on output.
export const SVG_CANONICAL: Record<string, string> = {
  viewbox: "viewBox",
  gradientunits: "gradientUnits",
  gradienttransform: "gradientTransform",
  stopcolor: "stopColor",
  textanchor: "textAnchor",
  fontsize: "fontSize",
  fontfamily: "fontFamily",
  fontweight: "fontWeight",
  fillopacity: "fillOpacity",
  strokewidth: "strokeWidth",
  strokeopacity: "strokeOpacity",
  preserveaspectratio: "preserveAspectRatio",
};
