/**
 * 题内路径规则：全部为纯字符串运算，绝不访问宿主文件系统，
 * 因此路径等价性与平台（大小写敏感与否、分隔符）无关。
 */

export const DEFAULT_EXTENSIONS = [
  ".ts",
  ".tsx",
  ".js",
  ".jsx",
  ".mjs",
  ".cjs",
  ".json",
];

/** 大小写折叠（Unicode 小写）。 */
export function caseFold(s: string): string {
  return s.toLowerCase();
}

/**
 * 词法规范化：统一分隔符、折叠 "." / ".."、去重复斜杠与末尾斜杠。
 * 不做任何 I/O；相对路径越出根时保留多余的 ".." 段。
 */
export function normalizePath(p: string): string {
  const unified = p.replace(/\\/g, "/");
  const isAbsolute = unified.startsWith("/");
  const out: string[] = [];
  for (const seg of unified.split("/")) {
    if (seg === "" || seg === ".") continue;
    if (seg === "..") {
      const top = out[out.length - 1];
      if (top !== undefined && top !== "..") {
        out.pop();
      } else if (!isAbsolute) {
        out.push("..");
      }
      continue;
    }
    out.push(seg);
  }
  const joined = out.join("/");
  return isAbsolute ? "/" + joined : joined;
}

export function joinPaths(base: string, rel: string): string {
  if (base === "") return normalizePath(rel);
  return normalizePath(base + "/" + rel);
}

export function dirName(p: string): string {
  const norm = normalizePath(p);
  const idx = norm.lastIndexOf("/");
  if (idx < 0) return "";
  if (idx === 0) return "/";
  return norm.slice(0, idx);
}

export function baseName(p: string): string {
  const norm = normalizePath(p);
  const idx = norm.lastIndexOf("/");
  return idx < 0 ? norm : norm.slice(idx + 1);
}

/** 词法相对路径：从 fromDir 指向 to，均为已规范化路径。 */
export function relativePath(fromDir: string, to: string): string {
  const from = fromDir.split("/").filter((s) => s !== "");
  const dest = to.split("/").filter((s) => s !== "");
  let common = 0;
  while (common < from.length && common < dest.length && from[common] === dest[common]) {
    common++;
  }
  const ups = from.length - common;
  const parts: string[] = [];
  for (let i = 0; i < ups; i++) parts.push("..");
  parts.push(...dest.slice(common));
  if (parts.length === 0) return ".";
  const joined = parts.join("/");
  return ups === 0 ? "./" + joined : joined;
}

interface AliasRule {
  /** 通配前缀（含尾部 "/"），非通配时为完整字面量。 */
  matchPrefix: string;
  targetPrefix: string;
  wildcard: boolean;
}

export function compileAliases(aliases: Record<string, string>): AliasRule[] {
  const rules: AliasRule[] = [];
  for (const [pattern, target] of Object.entries(aliases)) {
    if (pattern.endsWith("/*")) {
      rules.push({
        matchPrefix: pattern.slice(0, -1), // 保留尾部 "/"
        targetPrefix: normalizePath(target.replace(/\*$/, "")) + "/",
        wildcard: true,
      });
    } else {
      rules.push({
        matchPrefix: pattern,
        targetPrefix: normalizePath(target),
        wildcard: false,
      });
    }
  }
  // 最长前缀优先，保证确定性。
  rules.sort((a, b) => b.matchPrefix.length - a.matchPrefix.length);
  return rules;
}

/** 按别名表展开说明符/路径；无匹配时原样返回。最多展开一次。 */
export function expandAlias(specifier: string, rules: AliasRule[]): string {
  for (const rule of rules) {
    if (rule.wildcard) {
      if (specifier.startsWith(rule.matchPrefix)) {
        return normalizePath(rule.targetPrefix + specifier.slice(rule.matchPrefix.length));
      }
    } else if (specifier === rule.matchPrefix) {
      return rule.targetPrefix;
    }
  }
  return specifier;
}

export type { AliasRule };

/** 反复剥掉已知的可省略扩展名（大小写不敏感），如 "a.d.ts" -> "a.d"。 */
export function stripKnownExtensions(p: string, extensions: string[]): string {
  let out = p;
  for (;;) {
    const lower = caseFold(out);
    const hit = extensions.find((ext) => lower.endsWith(caseFold(ext)) && out.length > ext.length);
    if (!hit) return out;
    out = out.slice(0, out.length - hit.length);
  }
}

/** 模块身份键：规范化 + 别名展开 + 扩展名省略 + 大小写折叠。 */
export function canonicalKey(
  p: string,
  aliasRules: AliasRule[],
  extensions: string[],
): string {
  const expanded = expandAlias(normalizePath(p), aliasRules);
  return caseFold(stripKnownExtensions(expanded, extensions));
}

/** 路径的“实际身份”：规范化 + 别名展开，保留大小写与扩展名。 */
export function actualPath(p: string, aliasRules: AliasRule[]): string {
  return expandAlias(normalizePath(p), aliasRules);
}
