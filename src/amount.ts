/**
 * amount-input-core
 *
 * 金额输入核心库（与框架无关）：
 * - raw 值保留原始小数语义（中间态如 "-" 与尾点 "123." 合法）；
 * - display 在整数部分插入千位分隔符；
 * - 任意选区输入 / 删除 / 粘贴后，通过「原始字符位置映射」计算新光标，
 *   不依赖浏览器默认修正（调用方需做受控组件：拦截输入、回写 value 与 selection）。
 */

export interface PositionMap {
  /** rawToDisplay[r]：raw 光标 r（位于第 r 个逻辑字符之前）对应的 display 偏移 */
  rawToDisplay: number[];
  /** displayToRaw[d]：display 偏移 d 之前包含的逻辑字符（非逗号）数量 */
  displayToRaw: number[];
}

export interface EditResult {
  raw: string;
  display: string;
  /** 新光标在 display 中的偏移 */
  cursor: number;
}

export type DeleteDirection = 'none' | 'backward' | 'forward';

export interface EditOptions {
  /**
   * 删除内容仅为千位分隔符时的扩展方向：
   * - 'backward'（Backspace）：连同前一个逻辑字符一起删除；
   * - 'forward'（Delete）：连同后一个逻辑字符一起删除；
   * - 'none'：不扩展（默认，普通选区删除）。
   */
  deleteDirection: DeleteDirection;
}

const COMMA = ',';

/** 从任意文本中清洗出合法 raw：仅首位一个负号、至多一个小数点、其余为数字。 */
export function sanitizeRaw(text: string): string {
  let out = '';
  let seenDot = false;
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (ch >= '0' && ch <= '9') {
      out += ch;
    } else if (ch === '.' && !seenDot) {
      out += ch;
      seenDot = true;
    } else if (ch === '-' && out.length === 0) {
      out += ch;
    }
  }
  return out;
}

/** raw 是否为合法中间态：可选负号 + 数字 + 可选小数部分（允许 "-"、"123."、""）。 */
export function isValidRaw(raw: string): boolean {
  return /^-?\d*(\.\d*)?$/.test(raw);
}

/** 在 raw 的整数部分插入千位分隔符，负号、小数点与小数部分原样保留。 */
export function formatDisplay(raw: string): string {
  let sign = '';
  let body = raw;
  if (body.charAt(0) === '-') {
    sign = '-';
    body = body.slice(1);
  }
  const dotIndex = body.indexOf('.');
  const intPart = dotIndex === -1 ? body : body.slice(0, dotIndex);
  const fracPart = dotIndex === -1 ? '' : body.slice(dotIndex + 1);
  let grouped = '';
  for (let i = 0; i < intPart.length; i++) {
    if (i > 0 && (intPart.length - i) % 3 === 0) {
      grouped += COMMA;
    }
    grouped += intPart[i];
  }
  return sign + grouped + (dotIndex === -1 ? '' : '.' + fracPart);
}

/** 构建 display 与 raw 之间的双向光标映射（逗号不占逻辑位置）。 */
export function buildPositionMap(display: string): PositionMap {
  const displayToRaw: number[] = [];
  const rawToDisplay: number[] = [];
  let rawCount = 0;
  for (let i = 0; i < display.length; i++) {
    displayToRaw.push(rawCount);
    if (display[i] === COMMA) {
      continue;
    }
    rawToDisplay[rawCount] = i;
    rawCount++;
  }
  displayToRaw.push(rawCount);
  rawToDisplay[rawCount] = display.length;
  return { rawToDisplay: rawToDisplay, displayToRaw: displayToRaw };
}

interface SanitizeResult {
  raw: string;
  /** 光标（raw 偏移），随清洗过程同步修正 */
  cursor: number;
}

function sanitizeWithCursor(text: string, cursor: number): SanitizeResult {
  let raw = '';
  let seenDot = false;
  let kept = 0;
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    let keep = false;
    if (ch >= '0' && ch <= '9') {
      keep = true;
    } else if (ch === '.' && !seenDot) {
      seenDot = true;
      keep = true;
    } else if (ch === '-' && raw.length === 0) {
      keep = true;
    }
    if (keep) {
      raw += ch;
    }
    if (i < cursor && keep) {
      kept++;
    }
  }
  return { raw: raw, cursor: kept };
}

/**
 * 应用一次编辑：在 display 选区 [selStart, selEnd) 内插入 inserted（空串即删除）。
 * 返回新的 raw、display 与 display 坐标下的光标位置。
 *
 * 流程：display 选区 → raw 偏移 → 拼接 → 整体清洗（同步修正光标）→ 重新格式化
 * → raw 光标映射回 display。全程不依赖浏览器对 value 的默认修正。
 */
export function applyEdit(
  prevRaw: string,
  selStart: number,
  selEnd: number,
  inserted: string,
  options?: EditOptions
): EditResult {
  const prevDisplay = formatDisplay(prevRaw);
  const map = buildPositionMap(prevDisplay);
  const lo = Math.max(0, Math.min(Math.min(selStart, selEnd), prevDisplay.length));
  const hi = Math.max(0, Math.min(Math.max(selStart, selEnd), prevDisplay.length));
  let rawStart = map.displayToRaw[lo];
  let rawEnd = map.displayToRaw[hi];

  const direction: DeleteDirection = options ? options.deleteDirection : 'none';
  if (inserted === '' && lo !== hi && rawStart === rawEnd) {
    if (direction === 'backward' && rawStart > 0) {
      rawStart--;
    } else if (direction === 'forward' && rawEnd < prevRaw.length) {
      rawEnd++;
    }
  }

  const fragment = inserted.replace(/[^0-9.\-]/g, '');
  const candidate = prevRaw.slice(0, rawStart) + fragment + prevRaw.slice(rawEnd);
  const cursorCandidate = rawStart + fragment.length;
  const sanitized = sanitizeWithCursor(candidate, cursorCandidate);

  const display = formatDisplay(sanitized.raw);
  const nextMap = buildPositionMap(display);
  return {
    raw: sanitized.raw,
    display: display,
    cursor: nextMap.rawToDisplay[sanitized.cursor],
  };
}

/**
 * 提交态规范化（如 blur 时调用）：去掉中间态。
 * "-" → ""；"123." → "123"；".5" → "0.5"；"-.5" → "-0.5"；整数部分去前导零。
 */
export function normalizeRaw(raw: string): string {
  const clean = sanitizeRaw(raw);
  let sign = '';
  let body = clean;
  if (body.charAt(0) === '-') {
    sign = '-';
    body = body.slice(1);
  }
  const dotIndex = body.indexOf('.');
  let intPart = dotIndex === -1 ? body : body.slice(0, dotIndex);
  const fracPart = dotIndex === -1 ? '' : body.slice(dotIndex + 1);
  intPart = intPart.replace(/^0+(?=\d)/, '');
  if (intPart === '' && dotIndex !== -1) {
    intPart = '0';
  }
  if (intPart === '' && fracPart === '') {
    return '';
  }
  return sign + intPart + (fracPart === '' ? '' : '.' + fracPart);
}
