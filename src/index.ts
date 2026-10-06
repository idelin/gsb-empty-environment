import { Ast, countGroups, print } from './ast';
import { parse } from './parser';
import { exec, FullMatch } from './matcher';

export { Ast, countGroups, print } from './ast';
export { parse } from './parser';
export { enumerate, compare, exec } from './matcher';
export type { RawMatch, Caps, Span, FullMatch } from './matcher';

/** 一次提取的结果。groups[i] 对应第 i+1 个捕获组；未参与的组为 undefined（与空串 '' 不同）。 */
export interface ExtractResult {
  /** 匹配在输入中的起点（最左起点）。 */
  index: number;
  /** 整体匹配到的子串（同起点下最长）。 */
  match: string;
  /** 各捕获组内容；未参与为 undefined，参与但为空为 ''。 */
  groups: (string | undefined)[];
}

export interface CompiledPattern {
  readonly pattern: string;
  readonly ast: Ast;
  readonly groupCount: number;
  exec(input: string): ExtractResult | null;
}

/** 编译模式串，失败时抛 SyntaxError。 */
export function compile(pattern: string): CompiledPattern {
  const { ast, groupCount } = parse(pattern);
  return {
    pattern,
    ast,
    groupCount,
    exec(input: string): ExtractResult | null {
      const raw: FullMatch | null = exec(ast, groupCount, input);
      if (raw === null) return null;
      const groups: (string | undefined)[] = [];
      for (let g = 1; g <= groupCount; g++) {
        const span = raw.caps.get(g);
        groups.push(span === undefined ? undefined : input.slice(span[0], span[1]));
      }
      return { index: raw.start, match: input.slice(raw.start, raw.end), groups };
    },
  };
}

/** 便捷函数：编译并在一行日志上提取首个匹配。 */
export function extractFirst(pattern: string, logLine: string): ExtractResult | null {
  return compile(pattern).exec(logLine);
}
