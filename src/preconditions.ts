/**
 * 常用前置条件工厂。前置条件是“当前文件内容”的谓词，
 * 既在选择前检查，也会在每次应用修复后重新检查。
 */
import { ApplyContext, Precondition } from './types';

function toRegex(pattern: RegExp | string): RegExp {
  return typeof pattern === 'string' ? new RegExp(escapeRegExp(pattern)) : pattern;
}

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/** 文件中必须存在某段文本/模式。 */
export function textContains(file: string, pattern: RegExp | string, description?: string): Precondition {
  const re = toRegex(pattern);
  return {
    description: description ?? `文件 ${file} 中存在 ${re.source}`,
    files: [file],
    check(ctx: ApplyContext): boolean {
      const content = ctx.getFile(file);
      return content !== undefined && re.test(content);
    },
  };
}

/** 文件中必须不存在某段文本/模式。 */
export function textNotContains(file: string, pattern: RegExp | string, description?: string): Precondition {
  const re = toRegex(pattern);
  return {
    description: description ?? `文件 ${file} 中不存在 ${re.source}`,
    files: [file],
    check(ctx: ApplyContext): boolean {
      const content = ctx.getFile(file);
      return content !== undefined && !re.test(content);
    },
  };
}

/** 文件中必须仍定义着某个符号（按词边界匹配）。 */
export function symbolDefined(file: string, symbol: string, description?: string): Precondition {
  const re = new RegExp(`\\b${escapeRegExp(symbol)}\\b`);
  return {
    description: description ?? `符号 ${symbol} 在 ${file} 中仍有定义/引用`,
    files: [file],
    check(ctx: ApplyContext): boolean {
      const content = ctx.getFile(file);
      return content !== undefined && re.test(content);
    },
  };
}

/** 文件必须存在。 */
export function fileExists(file: string): Precondition {
  return {
    description: `文件 ${file} 存在`,
    files: [file],
    check(ctx: ApplyContext): boolean {
      return ctx.getFile(file) !== undefined;
    },
  };
}
