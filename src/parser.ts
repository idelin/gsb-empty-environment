import type { NpmrcEntry, NpmrcIssue } from './types.js';

export interface ParseResult {
  entries: NpmrcEntry[];
  issues: NpmrcIssue[];
}

/**
 * 解析 .npmrc 文本，采用 npm 兼容的“扁平键值”子集：
 * - 空行、; 和 # 注释、[section] 头部被忽略；
 * - 行以第一个 = 或首个空白分隔键值；
 * - 值两侧的成对单/双引号会被剥离；
 * - 行内 # 注释（其前至少一个空白）会被截断。
 */
export function parseNpmrc(text: string): ParseResult {
  const entries: NpmrcEntry[] = [];
  const issues: NpmrcIssue[] = [];
  const lines = text.split(/\r?\n/);

  lines.forEach((originalLine, index) => {
    const line = index + 1;
    const trimmed = originalLine.trim();
    if (trimmed === '' || trimmed.startsWith(';') || trimmed.startsWith('#')) {
      return;
    }
    if (trimmed.startsWith('[')) {
      return;
    }

    let working = trimmed;
    const hashIndex = working.search(/\s+#/);
    if (hashIndex !== -1) {
      working = working.slice(0, hashIndex).trimEnd();
    }

    const eqIndex = working.indexOf('=');
    let key: string;
    let value: string;
    if (eqIndex !== -1) {
      key = working.slice(0, eqIndex).trim();
      value = working.slice(eqIndex + 1).trim();
    } else {
      const spaceMatch = working.match(/^(\S+)\s+(.+)$/);
      if (!spaceMatch) {
        issues.push({
          code: 'malformed-line',
          line,
          key: working,
          message: `第 ${line} 行无法解析为 key=value：${working}`,
        });
        return;
      }
      key = spaceMatch[1].trim();
      value = spaceMatch[2].trim();
    }

    if (key === '') {
      issues.push({
        code: 'malformed-line',
        line,
        key,
        message: `第 ${line} 行缺少配置键：${working}`,
      });
      return;
    }

    entries.push({ line, key, value: stripQuotes(value), raw: working });
  });

  return { entries, issues };
}

function stripQuotes(value: string): string {
  if (value.length >= 2) {
    const first = value[0];
    const last = value[value.length - 1];
    if ((first === '"' && last === '"') || (first === "'" && last === "'")) {
      return value.slice(1, -1);
    }
  }
  return value;
}
