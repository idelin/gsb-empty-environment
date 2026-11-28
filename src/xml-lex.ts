import { XmlSecurityError } from './errors.ts';

/** XML S 产生式：空格、制表、回车、换行。 */
export function isXmlWhitespace(ch: string): boolean {
  return ch === ' ' || ch === '\t' || ch === '\r' || ch === '\n';
}

export function skipWhitespace(text: string, start: number): number {
  let i = start;
  while (i < text.length && isXmlWhitespace(text[i] as string)) {
    i++;
  }
  return i;
}

/** NameStartChar（含 ':'，覆盖 ASCII 与常用 Unicode 区段）。 */
export function isNameStartChar(ch: string): boolean {
  if (ch === '') {
    return false;
  }
  const cp = ch.codePointAt(0) as number;
  if (cp === 0x3a || cp === 0x5f) {
    return true;
  }
  if ((cp >= 0x41 && cp <= 0x5a) || (cp >= 0x61 && cp <= 0x7a)) {
    return true;
  }
  if (cp >= 0xc0 && cp <= 0xd7ff) {
    return true;
  }
  if (cp >= 0xe000 && cp <= 0xfffd) {
    return true;
  }
  if (cp >= 0x10000 && cp <= 0xeffff) {
    return true;
  }
  return false;
}

/** NameChar（含 '.'、'-'、数字与组合记号区段）。 */
export function isNameChar(ch: string): boolean {
  if (ch === '') {
    return false;
  }
  if (isNameStartChar(ch)) {
    return true;
  }
  const cp = ch.codePointAt(0) as number;
  if (cp === 0x2d || cp === 0x2e || cp === 0xb7) {
    return true;
  }
  if (cp >= 0x30 && cp <= 0x39) {
    return true;
  }
  if (cp >= 0x300 && cp <= 0x36f) {
    return true;
  }
  if (cp >= 0x203f && cp <= 0x2040) {
    return true;
  }
  return false;
}

export function readName(text: string, start: number): { name: string; next: number } {
  if (start >= text.length || !isNameStartChar(text[start] as string)) {
    return { name: '', next: start };
  }
  let i = start + 1;
  while (i < text.length && isNameChar(text[i] as string)) {
    i++;
  }
  return { name: text.slice(start, i), next: i };
}

export function startsWithCi(text: string, offset: number, token: string): boolean {
  return text.slice(offset, offset + token.length).toUpperCase() === token;
}

/** 跳过注释，禁止注释内容中出现 '--'，返回 '-->' 之后的位置。 */
export function skipComment(text: string, start: number): number {
  const end = text.indexOf('-->', start + 4);
  if (end < 0) {
    throw new XmlSecurityError('XML_MALFORMED', 'unterminated XML comment', start);
  }
  if (text.slice(start + 4, end).includes('--')) {
    throw new XmlSecurityError('XML_MALFORMED', "comment must not contain '--'", start);
  }
  return end + 3;
}

/** 跳过处理指令，返回 '?>' 之后的位置。 */
export function skipProcessingInstruction(text: string, start: number): number {
  const end = text.indexOf('?>', start + 2);
  if (end < 0) {
    throw new XmlSecurityError('XML_MALFORMED', 'unterminated processing instruction', start);
  }
  const target = readName(text, start + 2);
  if (target.name.toLowerCase() === 'xml' && start !== 0) {
    throw new XmlSecurityError('XML_MALFORMED', 'xml declaration is only allowed at document start', start);
  }
  return end + 2;
}
