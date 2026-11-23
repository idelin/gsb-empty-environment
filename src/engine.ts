/**
 * 最小模板引擎：单遍状态机扫描模板，并依据所在上下文自动选择转义规则。
 *
 * 语法：
 *  - {{ path }}   自动按当前上下文转义
 *  - {{{ path }}} 原始输出；脚本 (js) 上下文中直接拒绝
 *
 * 上下文推断：
 *  - <script> 内部为 js；<style> 内部为 css；注释内部为 comment
 *  - 标签内 href/src/action 等 URL 属性为 url
 *  - on* 事件属性为 js（值再经属性编码，保证引号闭合安全）
 *  - 其余属性为 attr，标签外的元素正文为 html
 */

import {
  encodeAttr,
  encodeJs,
  escape,
  SafeString,
  UnsafeRawOutputError,
  type EscapeContext,
} from './escaper.ts';

type TemplateNode =
  | { kind: 'text'; value: string }
  | {
      kind: 'interpolation';
      path: string;
      raw: boolean;
      context: EscapeContext;
      insideAttribute: boolean;
    };

const URL_ATTRIBUTES = new Set([
  'href',
  'src',
  'action',
  'formaction',
  'poster',
  'background',
  'cite',
  'data',
  'longdesc',
  'xlink:href',
]);

const RAW_OPEN = '{{{';
const RAW_CLOSE = '}}}';
const OPEN = '{{';
const CLOSE = '}}';

type ScannerState = 'text' | 'tag' | 'script' | 'style' | 'comment';

/** 编译模板为节点序列，每个插值均标注渲染时所在的上下文。 */
export function compile(template: string): TemplateNode[] {
  const nodes: TemplateNode[] = [];
  let index = 0;
  let textStart = 0;
  let state: ScannerState = 'text';

  const pushText = (end: number) => {
    if (end > textStart) {
      nodes.push({ kind: 'text', value: template.slice(textStart, end) });
    }
  };

  const readInterpolation = (
    position: number,
    raw: boolean,
    attributeContext?: EscapeContext,
  ): number => {
    const openToken = raw ? RAW_OPEN : OPEN;
    const doubleClose = template.indexOf(
      CLOSE,
      position + openToken.length,
    );
    const tripleClose = raw
      ? template.indexOf(RAW_CLOSE, position + openToken.length)
      : -1;
    const closeIndex =
      tripleClose === -1
        ? doubleClose
        : Math.min(doubleClose, tripleClose);
    const closeToken =
      closeIndex === tripleClose && tripleClose !== -1 ? RAW_CLOSE : CLOSE;
    if (closeIndex === -1) {
      return position + 1;
    }
    pushText(position);
    const interpolationEnd = closeIndex + closeToken.length;
    const path = template
      .slice(position + openToken.length, closeIndex)
      .trim();
    const context: EscapeContext =
      state === 'script'
        ? 'js'
        : state === 'style'
          ? 'css'
          : state === 'comment'
            ? 'comment'
            : attributeContext ?? 'html';
    const insideAttribute = attributeContext !== undefined;

    nodes.push({
      kind: 'interpolation',
      path,
      raw,
      context,
      insideAttribute,
    });
    textStart = interpolationEnd;
    return interpolationEnd;
  };

  while (index < template.length) {
    const rest = template.slice(index);

    if (state === 'comment') {
      if (rest.startsWith('-->')) {
        state = 'text';
        index += 3;
        textStart = index;
        continue;
      }
      if (rest.startsWith(RAW_OPEN)) {
        index = readInterpolation(index, true);
        continue;
      }
      if (rest.startsWith(OPEN)) {
        index = readInterpolation(index, false);
        continue;
      }
      index++;
      continue;
    }

    if (state === 'script' || state === 'style') {
      const closeTag = state === 'script' ? '</script' : '</style';
      if (rest.toLowerCase().startsWith(closeTag)) {
        pushText(index);
        state = 'text';
        continue;
      }
      if (rest.startsWith(RAW_OPEN)) {
        index = readInterpolation(index, true);
        continue;
      }
      if (rest.startsWith(OPEN)) {
        index = readInterpolation(index, false);
        continue;
      }
      index++;
      continue;
    }

    if (state === 'text') {
      if (rest.startsWith('<!--')) {
        pushText(index);
        state = 'comment';
        index += 4;
        textStart = index;
        continue;
      }
      if (rest.startsWith('</')) {
        const closeIndex = template.indexOf('>', index);
        if (closeIndex === -1) {
          index++;
          continue;
        }
        pushText(closeIndex + 1);
        index = closeIndex + 1;
        textStart = index;
        continue;
      }
      if (rest.startsWith('<') && /^<[a-zA-Z]/.test(rest)) {
        pushText(index);
        const parsed = parseTag(template, index, nodes);
        index = parsed.next;
        textStart = index;
        state = parsed.rawText === 'script'
          ? 'script'
          : parsed.rawText === 'style'
            ? 'style'
            : 'text';
        continue;
      }
      if (rest.startsWith(RAW_OPEN)) {
        index = readInterpolation(index, true);
        continue;
      }
      if (rest.startsWith(OPEN)) {
        index = readInterpolation(index, false);
        continue;
      }
      index++;
      continue;
    }
  }

  pushText(template.length);
  return nodes;
}

interface ParsedTag {
  next: number;
  rawText: 'script' | 'style' | undefined;
}

/** 解析单个开标签，把属性中的插值按 attr/url/js 上下文标注。 */
function parseTag(
  template: string,
  start: number,
  nodes: TemplateNode[],
): ParsedTag {
  const tagNameMatch = /^<\s*([a-zA-Z][-a-zA-Z0-9_]*)/.exec(
    template.slice(start),
  );
  const tagName = tagNameMatch ? tagNameMatch[1].toLowerCase() : '';
  let index = start;
  let currentAttribute = '';
  let pending = '';

  const flushText = () => {
    if (pending !== '') {
      nodes.push({ kind: 'text', value: pending });
      pending = '';
    }
  };

  const attributeContext = (): EscapeContext => {
    if (currentAttribute.startsWith('on')) return 'js';
    if (URL_ATTRIBUTES.has(currentAttribute)) return 'url';
    return 'attr';
  };

  while (index < template.length) {
    const char = template[index];

    if (char === '>') {
      flushText();
      nodes.push({ kind: 'text', value: '>' });
      return {
        next: index + 1,
        rawText: tagName === 'script' || tagName === 'style' ? tagName : undefined,
      };
    }

    if (char === '"' || char === "'") {
      const quote = char;
      pending += quote;
      index++;
      flushText();

      while (index < template.length && template[index] !== quote) {
        const isRaw = template.startsWith(RAW_OPEN, index);
        if (isRaw || template.startsWith(OPEN, index)) {
          const openToken = isRaw ? RAW_OPEN : OPEN;
          const doubleClose = template.indexOf(
            CLOSE,
            index + openToken.length,
          );
          const tripleClose = isRaw
            ? template.indexOf(RAW_CLOSE, index + openToken.length)
            : -1;
          const closeIndex =
            tripleClose === -1
              ? doubleClose
              : Math.min(doubleClose, tripleClose);
          const closeToken =
            closeIndex === tripleClose && tripleClose !== -1
              ? RAW_CLOSE
              : CLOSE;
          if (closeIndex === -1) {
            pending += template[index];
            index++;
            continue;
          }
          flushText();
          nodes.push({
            kind: 'interpolation',
            path: template.slice(index + openToken.length, closeIndex).trim(),
            raw: isRaw,
            context: attributeContext(),
            insideAttribute: true,
          });
          index = closeIndex + closeToken.length;
          continue;
        }
        pending += template[index];
        index++;
      }

      if (index < template.length && template[index] === quote) {
        pending += quote;
        index++;
      }
      flushText();
      continue;
    }

    if (char === '=') {
      const nameMatch = /([a-zA-Z_:][-a-zA-Z0-9_:.]*)$/.exec(
        pending,
      );
      currentAttribute = nameMatch ? nameMatch[1].toLowerCase() : '';
      pending += char;
      index++;
      continue;
    }

    pending += char;
    index++;
  }

  flushText();
  return { next: template.length, rawText: undefined };
}

/** 渲染模板；data 为插值变量表。 */
export function render(
  template: string,
  data: Record<string, unknown>,
): string {
  const nodes = compile(template);
  let output = '';

  for (const node of nodes) {
    if (node.kind === 'text') {
      output += node.value;
      continue;
    }

    const value = lookupPath(data, node.path);

    if (node.raw) {
      if (node.context === 'js') {
        throw new UnsafeRawOutputError(
          `Raw output of "${node.path}" is forbidden inside script context; ` +
            'use JSON-encoded output via {{ }} instead',
        );
      }
      output += value === undefined || value === null ? '' : String(value);
      continue;
    }

    if (node.context === 'js') {
      const literal = encodeJs(value);
      output += node.insideAttribute ? encodeAttr(literal) : literal;
      continue;
    }

    const encoded = escape(value, node.context);
    output += encoded.value;
  }

  return output;
}

function lookupPath(data: Record<string, unknown>, path: string): unknown {
  if (path === '') return undefined;
  let current: unknown = data;
  for (const segment of path.split('.')) {
    if (current === null || current === undefined) {
      return undefined;
    }
    current = (current as Record<string, unknown>)[segment];
  }
  return current;
}

export { SafeString };
