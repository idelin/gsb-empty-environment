import { createHash } from 'node:crypto';

/**
 * 归一化语句源码：去掉行注释与多余空白，使“移动位置 / 改变换行”
 * 不改变指纹，而任何实质内容变化都会改变指纹。
 */
export function normalizeSource(text: string): string {
  return text
    .replace(/\/\/[^\n]*/g, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/\s+/g, ' ')
    .trim();
}

/** 由语句源码计算内容指纹。 */
export function fingerprintOf(sourceText: string): string {
  return createHash('sha256').update(normalizeSource(sourceText), 'utf8').digest('hex').slice(0, 16);
}
