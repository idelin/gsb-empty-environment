import { NS_HTML, NS_MATHML, NS_SVG } from './types.ts';
import type { Namespace } from './types.ts';

/** Tags that must be removed together with their entire subtree. */
export const HARD_STRIPPED_TAGS = new Set([
  'script', 'style', 'iframe', 'object', 'embed', 'base', 'meta',
  'noscript', 'link', 'title', 'template', 'frame', 'frameset',
  'noembed', 'noframes', 'xmp', 'plaintext',
]);

const HTML_ALLOWED_TAGS = new Set([
  'a', 'abbr', 'address', 'article', 'aside', 'b', 'bdi', 'bdo',
  'blockquote', 'br', 'caption', 'cite', 'code', 'col', 'colgroup',
  'dd', 'del', 'details', 'dfn', 'div', 'dl', 'dt', 'em', 'figcaption',
  'figure', 'footer', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'header',
  'hgroup', 'hr', 'i', 'img', 'ins', 'kbd', 'li', 'main', 'mark', 'nav',
  'ol', 'p', 'picture', 'pre', 'q', 'rp', 'rt', 'ruby', 's', 'samp',
  'section', 'small', 'source', 'span', 'strong', 'sub', 'summary', 'sup',
  'table', 'tbody', 'td', 'tfoot', 'th', 'thead', 'time', 'tr', 'track',
  'u', 'ul', 'var', 'wbr',
]);

const SVG_ALLOWED_TAGS = new Set([
  'a', 'animate', 'animateMotion', 'animateTransform', 'circle',
  'clipPath', 'defs', 'desc', 'ellipse', 'feBlend', 'feColorMatrix',
  'feComponentTransfer', 'feComposite', 'feConvolveMatrix',
  'feDiffuseLighting', 'feDisplacementMap', 'feDistantLight', 'feFlood',
  'feFuncA', 'feFuncB', 'feFuncG', 'feFuncR', 'feGaussianBlur', 'feImage',
  'feMerge', 'feMergeNode', 'feMorphology', 'feOffset', 'fePointLight',
  'feSpecularLighting', 'feSpotLight', 'feTile', 'feTurbulence', 'filter',
  'foreignObject', 'g', 'image', 'line', 'linearGradient', 'marker',
  'mask', 'metadata', 'mpath', 'path', 'pattern', 'polygon', 'polyline',
  'radialGradient', 'rect', 'set', 'stop', 'svg', 'switch', 'symbol',
  'text', 'textPath', 'title', 'tspan', 'use', 'view',
]);

const MATHML_ALLOWED_TAGS = new Set([
  'annotation', 'annotation-xml', 'maction', 'math', 'merror', 'mfrac',
  'mi', 'mmultiscripts', 'mn', 'mo', 'mover', 'mpadded', 'mphantom',
  'mprescripts', 'mroot', 'mrow', 'ms', 'mspace', 'msqrt', 'mstyle',
  'msub', 'msubsup', 'msup', 'mtable', 'mtd', 'mtext', 'mtr', 'munder',
  'munderover', 'semantics',
]);

export function isAllowedTag(tagName: string, namespace: Namespace): boolean {
  if (namespace === NS_HTML) {
    return HTML_ALLOWED_TAGS.has(tagName);
  }
  if (namespace === NS_SVG) {
    return SVG_ALLOWED_TAGS.has(tagName);
  }
  return MATHML_ALLOWED_TAGS.has(tagName);
}

/** Global HTML attributes allowed on every HTML element. */
const GLOBAL_HTML_ATTRIBUTES = new Set([
  'class', 'dir', 'hidden', 'id', 'lang', 'role', 'tabindex', 'title',
  'translate', 'xml:lang',
]);

const GLOBAL_SVG_ATTRIBUTES = new Set([
  'class', 'id', 'lang', 'tabindex', 'xml:lang', 'xmlns', 'xmlns:xlink',
]);

const GLOBAL_MATHML_ATTRIBUTES = new Set([
  'class', 'id', 'dir', 'mathvariant', 'mathsize', 'displaystyle',
  'xmlns',
]);

const TAG_SPECIFIC_HTML_ATTRIBUTES: Readonly<Record<string, ReadonlySet<string>>> = {
  a: new Set(['href', 'hreflang', 'rel', 'target', 'type']),
  img: new Set(['alt', 'height', 'src', 'srcset', 'width', 'loading']),
  source: new Set(['src', 'srcset', 'type', 'media', 'sizes']),
  track: new Set(['src', 'srclang', 'label', 'kind', 'default']),
  blockquote: new Set(['cite']),
  q: new Set(['cite']),
  ins: new Set(['cite', 'datetime']),
  del: new Set(['cite', 'datetime']),
  time: new Set(['datetime']),
  col: new Set(['span']),
  colgroup: new Set(['span']),
  td: new Set(['colspan', 'rowspan', 'headers']),
  th: new Set(['colspan', 'rowspan', 'headers', 'scope', 'abbr']),
  ol: new Set(['reversed', 'start', 'type']),
  li: new Set(['value']),
  details: new Set(['open']),
  summary: new Set([]),
};

const SVG_PRESENTATION_ATTRIBUTES = new Set([
  'alignment-baseline', 'baseline-shift', 'clip-path', 'clip-rule',
  'color', 'color-interpolation', 'color-interpolation-filters',
  'color-profile', 'color-rendering', 'cursor', 'direction',
  'display', 'dominant-baseline', 'enable-background', 'fill',
  'fill-opacity', 'fill-rule', 'filter', 'flood-color',
  'flood-opacity', 'font-family', 'font-size', 'font-size-adjust',
  'font-stretch', 'font-style', 'font-variant', 'font-weight',
  'glyph-orientation-horizontal', 'glyph-orientation-vertical',
  'image-rendering', 'kerning', 'letter-spacing', 'lighting-color',
  'marker-end', 'marker-mid', 'marker-start', 'opacity', 'overflow',
  'pointer-events', 'shape-rendering', 'stop-color', 'stop-opacity',
  'stroke', 'stroke-dasharray', 'stroke-dashoffset', 'stroke-linecap',
  'stroke-linejoin', 'stroke-miterlimit', 'stroke-opacity',
  'stroke-width', 'text-anchor', 'text-decoration', 'text-rendering',
  'unicode-bidi', 'visibility', 'word-spacing', 'writing-mode',
]);

const SVG_STRUCTURAL_ATTRIBUTES = new Set([
  'd', 'pathLength', 'x', 'y', 'x1', 'y1', 'x2', 'y2', 'cx', 'cy', 'r',
  'rx', 'ry', 'width', 'height', 'viewBox', 'preserveAspectRatio', 'xlink:href',
  'href', 'points', 'offset', 'gradientUnits', 'gradientTransform',
  'patternUnits', 'patternContentUnits', 'patternTransform',
  'markerUnits', 'markerWidth', 'markerHeight', 'refX', 'refY',
  'orient', 'spreadMethod', 'fx', 'fy', 'fr', 'maskUnits',
  'maskContentUnits', 'clipPathUnits', 'primitiveUnits', 'stdDeviation',
  'tableValues', 'slope', 'intercept', 'amplitude', 'exponent',
  'kernelMatrix', 'kernelUnitLength', 'edgeMode', 'preserveAlpha',
  'surfaceScale', 'diffuseConstant', 'specularConstant',
  'specularExponent', 'limitingConeAngle', 'feColorMatrix-type',
  'in', 'in2', 'result', 'mode', 'operator', 'values', 'keyTimes',
  'keySplines', 'keyPoints', 'calcMode', 'repeatCount', 'repeatDur',
  'begin', 'end', 'dur', 'min', 'max', 'restart', 'fill', 'accumulate',
  'additive', 'attributeName', 'attributeType', 'from', 'to', 'by',
  'systemLanguage', 'transform', 'contentScriptType', 'contentStyleType',
  'zoomAndPan', 'viewTarget', 'externalResourcesRequired', 'focusable',
  'textLength', 'lengthAdjust', 'startOffset', 'method', 'spacing',
  'glyphRef', 'format', 'local', 'name', 'media',
]);

const MATHML_ATTRIBUTES = new Set([
  'accent', 'accentunder', 'bevelled', 'close', 'columnalign',
  'columnlines', 'columnspan', 'denomalign', 'depth', 'encoding',
  'fence', 'frame', 'largeop', 'length', 'linethickness', 'lspace',
  'lquote', 'mathbackground', 'mathcolor', 'numalign', 'open',
  'rowalign', 'rowlines', 'rowspacing', 'rowspan', 'rspace', 'rquote',
  'scriptlevel', 'scriptminsize', 'scriptsizemultiplier', 'selection',
  'separator', 'separators', 'stretchy', 'subscriptshift',
  'superscriptshift', 'symmetric', 'voffset', 'notation', 'open',
  'close', 'definitionURL', 'type', 'src', 'href', 'alt', 'altimg',
  'alttext', 'cdgroup', 'cd', 'name',
]);

/** Attributes whose value must pass the URL-scheme guard. */
export const URI_ATTRIBUTES = new Set([
  'href', 'src', 'xlink:href', 'cite', 'poster', 'background',
  'longdesc', 'usemap', 'profile', 'action', 'formaction', 'data',
]);

export function isAllowedAttribute(
  tagName: string,
  namespace: Namespace,
  attributeName: string,
): boolean {
  if (attributeName.startsWith('on')) {
    return false;
  }
  if (namespace === NS_HTML && attributeName === 'style') {
    // Gated separately by SanitizeOptions.allowStyleAttribute.
    return true;
  }
  if (attributeName.startsWith('data-') && namespace === NS_HTML) {
    return !attributeName.includes(':');
  }
  if (attributeName.startsWith('aria-')) {
    return true;
  }
  if (namespace === NS_HTML) {
    if (GLOBAL_HTML_ATTRIBUTES.has(attributeName)) {
      return true;
    }
    return TAG_SPECIFIC_HTML_ATTRIBUTES[tagName]?.has(attributeName) ?? false;
  }
  if (namespace === NS_SVG) {
    return GLOBAL_SVG_ATTRIBUTES.has(attributeName) ||
      SVG_PRESENTATION_ATTRIBUTES.has(attributeName) ||
      SVG_STRUCTURAL_ATTRIBUTES.has(attributeName);
  }
  return GLOBAL_MATHML_ATTRIBUTES.has(attributeName) ||
    MATHML_ATTRIBUTES.has(attributeName);
}

/** Allowed URL schemes (everything else, including data:, is rejected). */
export const DEFAULT_ALLOWED_PROTOCOLS = new Set([
  'http:', 'https:', 'mailto:', 'tel:', 'ftp:', 'ftps:',
]);
