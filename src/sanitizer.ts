import {
  COMMENT_NODE,
  ELEMENT_NODE,
  HTML_NS,
  type DomHost,
  type MiniElement,
  type MiniNode,
  type SanitizeResult,
} from './types.ts';
import {
  ALLOWED_ELEMENTS,
  URL_ATTRS,
  isAllowedAttribute,
  isSafeUrl,
  normalizeAttribute,
} from './whitelist.ts';

function ok(fragment: MiniNode): SanitizeResult {
  return { fragment, dropped: false, reason: 'ok' };
}

function dropped(host: DomHost): SanitizeResult {
  return {
    fragment: host.createFragment(),
    dropped: true,
    reason: 'round-trip-mismatch',
  };
}

/**
 * 结构相等比较：元素按命名空间 + localName 比较，属性按集合比较（忽略
 * 宿主序列化器可能重排的属性顺序），文本按 data 精确比较。任何差异都
 * 视为往返不稳定——这正是 mXSS 的温床，宁可整段丢弃。
 */
function sameTree(a: MiniNode, b: MiniNode, host: DomHost): boolean {
  if (a.nodeType !== b.nodeType) {
    return false;
  }
  if (host.isText(a)) {
    return host.isText(b) && a.textContent === b.textContent;
  }
  if (a.nodeType === COMMENT_NODE || b.nodeType === COMMENT_NODE) {
    // 消毒阶段注释已全部移除，两边都不应出现。
    return false;
  }
  // 文档片段等容器节点：只比较子树。
  if (!host.isElement(a) || !host.isElement(b)) {
    if (host.isElement(a) || host.isElement(b)) {
      return false;
    }
  } else {
    const ea = a as MiniElement;
    const eb = b as MiniElement;
    if (ea.namespaceURI !== eb.namespaceURI || ea.localName !== eb.localName) {
      return false;
    }
    const namesA = ea.getAttributeNames().slice().sort();
    const namesB = eb.getAttributeNames().slice().sort();
    if (namesA.length !== namesB.length ||
        namesA.some((name, i) => name !== namesB[i])) {
      return false;
    }
    if (namesA.some((name) => ea.getAttribute(name) !== eb.getAttribute(name))) {
      return false;
    }
  }
  const kidsA = a.childNodes;
  const kidsB = b.childNodes;
  if (kidsA.length !== kidsB.length) {
    return false;
  }
  return kidsA.every((child, i) => sameTree(child, kidsB[i], host));
}

/**
 * 递归消毒单个节点，返回安全节点；无法保留时返回 null。
 * - 非 HTML 命名空间元素（svg/math/foreignObject 及其子树）整棵移除；
 * - 非白名单元素整棵移除（连同子树，避免标签剥离后语义被重新解释）；
 * - 注释、处理指令等非元素/文本节点移除；
 * - style 元素与 style 属性不在白名单，天然被移除。
 */
function sanitizeNode(
  source: MiniNode,
  host: DomHost,
): MiniNode | null {
  if (host.isText(source)) {
    return host.createTextNode(source.textContent);
  }
  if (!host.isElement(source) || source.nodeType !== ELEMENT_NODE) {
    return null;
  }
  const element = source as MiniElement;
  if (element.namespaceURI !== HTML_NS) {
    return null;
  }
  const tag = element.localName.toLowerCase();
  if (!ALLOWED_ELEMENTS.has(tag)) {
    return null;
  }
  const clean = host.createElement(tag);
  for (const rawName of element.getAttributeNames()) {
    const name = rawName.toLowerCase();
    if (!isAllowedAttribute(tag, name)) {
      continue;
    }
    const rawValue = element.getAttribute(rawName);
    if (rawValue === null) {
      continue;
    }
    const value = normalizeAttribute(name, rawValue);
    if (URL_ATTRS.has(name) && !isSafeUrl(value)) {
      continue;
    }
    clean.setAttribute(name, value);
  }
  for (const child of source.childNodes) {
    const cleanChild = sanitizeNode(child, host);
    if (cleanChild !== null) {
      host.appendChild(clean, cleanChild);
    }
  }
  mergeAdjacentText(clean, host);
  return clean;
}

/**
 * 合并相邻文本节点。删除注释/恶意元素后常常留下两个相邻文本节点，
 * 而 HTML 解析器重新解析序列化结果时只会产生一个，若不归一化就会
 * 触发误判，甚至被利用构造 DOM 分裂型 mXSS。
 */
function mergeAdjacentText(parent: MiniNode, host: DomHost): void {
  const children = parent.childNodes;
  for (let i = 0; i < children.length - 1;) {
    if (host.isText(children[i]) && host.isText(children[i + 1])) {
      const first = children[i];
      const second = children[i + 1];
      first.textContent += second.textContent;
      host.removeChild(parent, second);
      continue;
    }
    i += 1;
  }
}

/**
 * 粘贴消毒入口。
 *
 * 流程：克隆输入 -> 白名单过滤 -> 序列化 -> 重新解析 -> 结构比对。
 * 重新解析后的树与消毒树有任何不一致，说明该载荷在当前解析器下存在
 * 往返不稳定（典型 mXSS 前提），整段丢弃，绝不把“清理后”的字符串
 * 直接交给编辑器的另一条解析路径。
 */
export function sanitizePaste(root: MiniNode, host: DomHost): SanitizeResult {
  let cloned: MiniNode;
  try {
    cloned = host.clone(root);
  } catch {
    return {
      fragment: host.createFragment(),
      dropped: true,
      reason: 'unsupported-input',
    };
  }

  const output = host.createFragment();
  const sources = cloned.nodeType === ELEMENT_NODE ? [cloned] : cloned.childNodes;
  for (const source of sources.slice()) {
    const clean = sanitizeNode(source, host);
    if (clean !== null) {
      host.appendChild(output, clean);
    }
  }
  mergeAdjacentText(output, host);

  const serialized = host.serialize(output);
  const reparsed = host.parseHTML(serialized);
  if (!sameTree(output, reparsed, host)) {
    return dropped(host);
  }
  return ok(output);
}
