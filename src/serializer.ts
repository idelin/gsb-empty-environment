import type {
  AnyNode,
  Attribute,
  ChildNode,
  ElementNode,
} from './types.ts';
import { NS_HTML } from './types.ts';

const HTML_VOID_ELEMENTS = new Set([
  'area', 'base', 'br', 'col', 'embed', 'hr', 'img', 'input', 'link',
  'meta', 'param', 'source', 'track', 'wbr',
]);

const HTML_RAWTEXT_ELEMENTS = new Set(['style', 'script', 'xmp', 'iframe',
  'noembed', 'noframes', 'noscript']);

const escapeText = (value: string): string =>
  value
    .replaceAll('&', '&amp;')
    .replaceAll('\u00A0', '&nbsp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;');

const escapeAttribute = (value: string): string =>
  escapeText(value)
    .replaceAll('"', '&quot;');

/**
 * Deterministic HTML5 fragment serializer ("serializing HTML fragments").
 * Output is canonical so repeated parse/serialize cycles converge after a
 * single pass; foreign leaf nodes always use explicit self-closing syntax to
 * avoid a browser re-parsing them as open HTML-style elements.
 */
export function serialize(node: AnyNode): string {
  switch (node.type) {
    case 'document':
      return node.children.map(serializeChild).join('');
    case 'text':
      return escapeText(node.data);
    case 'comment':
      return `<!--${node.data}-->`;
    case 'element':
      return serializeElement(node);
  }
}

function serializeChild(node: ChildNode): string {
  return serialize(node);
}

function serializeElement(element: ElementNode): string {
  const opening =
    `<${element.tagName}${element.attributes.map(serializeAttribute).join('')}`;

  if (element.namespace === NS_HTML &&
    HTML_VOID_ELEMENTS.has(element.tagName)) {
    return `${opening}>`;
  }

  if (element.namespace !== NS_HTML &&
    element.selfClosing &&
    element.children.length === 0) {
    return `${opening}/>`;
  }

  if (element.namespace === NS_HTML &&
    HTML_RAWTEXT_ELEMENTS.has(element.tagName)) {
    const inner = element.children
      .map((child) => child.type === 'text' ? child.data : serialize(child))
      .join('');
    return `${opening}>${inner}</${element.tagName}>`;
  }

  const inner = element.children.map(serializeChild).join('');
  return `${opening}>${inner}</${element.tagName}>`;
}

function serializeAttribute(attribute: Attribute): string {
  return ` ${attribute.name}="${escapeAttribute(attribute.value)}"`;
}
