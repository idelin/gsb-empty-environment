import {
  AliasRule,
  actualPath,
  canonicalKey,
  dirName,
  joinPaths,
  normalizePath,
  relativePath,
  stripKnownExtensions,
  baseName,
} from "./pathRules";

export interface IndexedModule {
  /** 输入中的原始路径。 */
  inputPath: string;
  /** 规范化 + 别名展开后的实际路径（保留大小写）。 */
  actual: string;
  key: string;
  renameCost: number;
}

export function indexModules(
  modules: { path: string; renameCost?: number }[],
  aliasRules: AliasRule[],
  extensions: string[],
): IndexedModule[] {
  return modules.map((m) => {
    const actual = actualPath(m.path, aliasRules);
    return {
      inputPath: m.path,
      actual,
      key: canonicalKey(m.path, aliasRules, extensions),
      renameCost: m.renameCost ?? 1,
    };
  });
}

export type Resolution =
  | { kind: "ok"; module: IndexedModule }
  | { kind: "ambiguous"; candidates: IndexedModule[] }
  | { kind: "unresolved" }
  | { kind: "external" };

export function isRelativeSpecifier(specifier: string): boolean {
  return specifier.startsWith("./") || specifier.startsWith("../") || specifier === "." || specifier === "..";
}

/**
 * 将引用说明符解析到模块。
 * 相对说明符基于 importer 目录解析；其余先尝试别名展开，
 * 无法展开者视为外部包（不参与冲突检测）。
 * 多个候选时，大小写敏感的精确路径匹配优先。
 */
export function resolveSpecifier(
  specifier: string,
  fromActualDir: string,
  modules: IndexedModule[],
  aliasRules: AliasRule[],
  extensions: string[],
): Resolution {
  let target: string;
  if (isRelativeSpecifier(specifier)) {
    target = joinPaths(fromActualDir, specifier);
  } else {
    const expanded = expandAliasOnce(specifier, aliasRules);
    if (expanded === specifier) return { kind: "external" };
    target = expanded;
  }
  const key = canonicalKey(target, aliasRules, extensions);
  const candidates = modules.filter((m) => m.key === key);
  if (candidates.length === 0) return { kind: "unresolved" };
  if (candidates.length === 1) return { kind: "ok", module: candidates[0] };
  // 多个候选（冲突组）时按大小写敏感的精确匹配消歧：
  // 先比完整路径，再比省略扩展名后的路径。
  const normTarget = normalizePath(target);
  const exactFull = candidates.filter((m) => m.actual === normTarget);
  if (exactFull.length === 1) return { kind: "ok", module: exactFull[0] };
  const strippedTarget = stripKnownExtensions(normTarget, extensions);
  const exactStem = candidates.filter(
    (m) => stripKnownExtensions(m.actual, extensions) === strippedTarget,
  );
  if (exactStem.length === 1) return { kind: "ok", module: exactStem[0] };
  return { kind: "ambiguous", candidates };
}

function expandAliasOnce(specifier: string, rules: AliasRule[]): string {
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

/**
 * 为目标模块渲染新的说明符，尽量保持原说明符风格：
 * - 相对引用仍渲染为相对路径；别名引用仍渲染为别名形式；
 * - 原说明符省略扩展名则新说明符同样省略。
 */
export function renderSpecifier(
  targetActual: string,
  fromActualDir: string,
  originalSpecifier: string,
  aliasRules: AliasRule[],
  extensions: string[],
): string {
  const hadExtension = hasKnownExtension(baseName(originalSpecifier), extensions);
  const maybeStrip = (p: string) => (hadExtension ? p : stripKnownExtensions(p, extensions));

  if (isRelativeSpecifier(originalSpecifier)) {
    return maybeStrip(relativePath(fromActualDir, targetActual));
  }
  // 别名引用：选择目标前缀最长匹配的别名重建。
  let best: { rule: AliasRule; remainder: string } | undefined;
  for (const rule of aliasRules) {
    if (!rule.wildcard) continue;
    if (targetActual.startsWith(rule.targetPrefix)) {
      const remainder = targetActual.slice(rule.targetPrefix.length);
      if (!best || rule.targetPrefix.length > best.rule.targetPrefix.length) {
        best = { rule, remainder };
      }
    }
  }
  if (best) {
    return maybeStrip(best.rule.matchPrefix + best.remainder);
  }
  return maybeStrip(relativePath(fromActualDir, targetActual));
}

function hasKnownExtension(name: string, extensions: string[]): boolean {
  const lower = name.toLowerCase();
  return extensions.some((ext) => lower.endsWith(ext.toLowerCase()) && name.length > ext.length);
}

export { dirName };
