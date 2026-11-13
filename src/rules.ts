import type { PathRule, ProtectionClass } from './types.ts';
import { strongerOf } from './classes.ts';

/** 规整路径：去掉尾部多余的 `/`，但保留根目录 `/`。 */
export function normalizePath(path: string): string {
  if (path.length > 1) {
    return path.replace(/\/+$/, '');
  }
  return path;
}

/**
 * 判断 `path` 是否落在 `prefix` 之下。
 * 命中条件：与前缀完全相同，或位于前缀目录内（前缀后紧跟 `/`）。
 * 例如 prefix=/a 命中 /a、/a/b，但不会误命中 /ab。
 */
export function matchesPrefix(path: string, prefix: string): boolean {
  const target = normalizePath(path);
  const base = normalizePath(prefix);
  if (base === '/') {
    return target.startsWith('/');
  }
  return target === base || target.startsWith(`${base}/`);
}

/**
 * 返回路径命中的所有规则中最强的要求类。
 * 一条路径落在多条规则下时取最强要求；无任何命中时返回 undefined。
 */
export function strongestRequirement(
  path: string,
  rules: readonly PathRule[],
): ProtectionClass | undefined {
  let required: ProtectionClass | undefined;
  for (const rule of rules) {
    if (!matchesPrefix(path, rule.prefix)) {
      continue;
    }
    required =
      required === undefined
        ? rule.minClass
        : strongerOf(required, rule.minClass);
  }
  return required;
}
