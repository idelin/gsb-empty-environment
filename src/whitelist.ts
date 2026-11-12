/**
 * 粘贴白名单。只允许承载内容语义的 HTML 元素：
 * - 不含 script/iframe/object/embed/form/input/template/link/base 等可执行或
 *   可改变文档基址/提交行为的元素；
 * - 不含 style 元素（样式一律移除）；
 * - 命名空间层面只接受 HTML，svg/math/foreignObject 在核心流程中整体移除。
 */

/** 所有白名单元素都允许的全局属性。data-* 走单独前缀规则。 */
export const GLOBAL_ATTRS: ReadonlySet<string> = new Set([
  'title',
  'lang',
  'dir',
]);

/**
 * 元素专属属性白名单。键为 HTML localName（小写）。
 * 注意：style 属性不在任何白名单中，即样式一律移除。
 */
export const ELEMENT_ATTRS: Readonly<Record<string, readonly string[]>> = {
  a: ['href', 'title', 'rel'],
  img: ['src', 'alt', 'width', 'height'],
  br: [],
  p: [],
  hr: [],
  span: [],
  div: [],
  blockquote: ['cite'],
  pre: [],
  code: [],
  ul: [],
  ol: ['start', 'reversed', 'type'],
  li: ['value'],
  dl: [],
  dt: [],
  dd: [],
  h1: [],
  h2: [],
  h3: [],
  h4: [],
  h5: [],
  h6: [],
  b: [],
  strong: [],
  i: [],
  em: [],
  u: [],
  s: [],
  sub: [],
  sup: [],
  mark: [],
  small: [],
  abbr: ['title'],
  cite: [],
  kbd: [],
  q: ['cite'],
  table: [],
  thead: [],
  tbody: [],
  tfoot: [],
  tr: [],
  td: ['colspan', 'rowspan'],
  th: ['colspan', 'rowspan', 'scope'],
  colgroup: ['span'],
  col: ['span'],
  caption: [],
  figure: [],
  figcaption: [],
  picture: [],
  source: ['src', 'srcset', 'type'],
  video: ['src', 'controls', 'width', 'height', 'poster'],
  audio: ['src', 'controls'],
  time: ['datetime'],
};

export const ALLOWED_ELEMENTS: ReadonlySet<string> = new Set(
  Object.keys(ELEMENT_ATTRS),
);

/** 值必须是安全 URL 的属性。 */
export const URL_ATTRS: ReadonlySet<string> = new Set([
  'href',
  'src',
  'cite',
  'poster',
]);

/**
 * 浏览器解析属性值时会从 URL 属性中剥离的字符（HTML 规范的
 * strip-newlines 规则覆盖 tab/LF/CR）。消毒时做同样的归一化，
 * 保证“校验时看到的值”和“序列化再解析后的值”一致，避免因
 * 归一化差异制造往返不稳定。
 */
function normalizeUrlValue(value: string): string {
  return value.replace(/[\t\n\r\f]/g, '').trim();
}

/**
 * 仅允许 http、https、mailto 三种 scheme，且必须是绝对 URL。
 * 相对 URL、协议相对 URL（//evil.test）、javascript:/data:/vbscript:、
 * 实体/控制字符混淆的 scheme 一律拒绝。
 */
export function isSafeUrl(rawValue: string): boolean {
  const value = normalizeUrlValue(rawValue);
  if (value.length === 0) {
    return false;
  }
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return false;
  }
  const scheme = parsed.protocol.replace(/:$/, '').toLowerCase();
  if (scheme === 'http' || scheme === 'https') {
    return parsed.hostname.length > 0;
  }
  if (scheme === 'mailto') {
    // mailto: 没有 host，路径部分必须非空（拒绝裸 mailto:）；
    // 不强制邮箱形状，以便兼容带 ?subject= 等查询参数的合法地址。
    return parsed.pathname.trim().length > 0;
  }
  return false;
}

/** 返回 URL 属性归一化后的值；非 URL 属性原样返回。 */
export function normalizeAttribute(name: string, value: string): string {
  return URL_ATTRS.has(name) ? normalizeUrlValue(value) : value;
}

/** 属性是否在白名单内（含 data-* 前缀规则）。 */
export function isAllowedAttribute(tag: string, name: string): boolean {
  if (GLOBAL_ATTRS.has(name)) {
    return true;
  }
  if (name.startsWith('data-') && name.length > 'data-'.length) {
    return true;
  }
  const perElement = ELEMENT_ATTRS[tag];
  return Array.isArray(perElement) && perElement.includes(name);
}
