import {
  DEFAULT_EXTENSIONS,
  baseName,
  compileAliases,
  dirName,
} from "./pathRules";
import {
  IndexedModule,
  indexModules,
  renderSpecifier,
  resolveSpecifier,
} from "./resolver";
import {
  AnalysisResult,
  AnalyzerInput,
  BadReference,
  ConflictGroup,
  ReferenceUpdate,
  RenameAction,
} from "./types";

/**
 * 主流程：冲突检测 -> 最小代价改名规划 -> 引用同步更新。
 */
export function analyze(input: AnalyzerInput): AnalysisResult {
  const extensions = input.extensions ?? DEFAULT_EXTENSIONS;
  const aliasRules = compileAliases(input.aliases ?? {});
  const modules = indexModules(input.modules, aliasRules, extensions);

  const conflicts = detectConflicts(modules);
  const renames = planRenames(modules, conflicts, extensions);
  const totalCost = renames.reduce((sum, r) => sum + r.cost, 0);

  const renamedTo = new Map(renames.map((r) => [r.from, r.to]));
  const finalModules: IndexedModule[] = modules.map((m) => {
    const next = renamedTo.get(m.actual);
    return next ? { ...m, actual: next } : m;
  });

  const { updatedReferences, badReferences } = updateReferences(
    input,
    modules,
    finalModules,
    renamedTo,
    aliasRules,
    extensions,
  );

  return { conflicts, renames, totalCost, updatedReferences, badReferences };
}

/** 按规范化键分组；键相同而实际路径不唯一的组即为冲突。 */
export function detectConflicts(modules: IndexedModule[]): ConflictGroup[] {
  const byKey = new Map<string, IndexedModule[]>();
  for (const m of modules) {
    const group = byKey.get(m.key);
    if (group) group.push(m);
    else byKey.set(m.key, [m]);
  }
  const conflicts: ConflictGroup[] = [];
  for (const [key, group] of byKey) {
    const distinct = new Set(group.map((m) => m.actual));
    if (distinct.size > 1) {
      conflicts.push({ key, modules: group.map((m) => m.actual) });
    }
  }
  conflicts.sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
  return conflicts;
}

/**
 * 最小代价改名：各冲突组相互独立，每组保留改名代价最高者
 * （并列时保留输入顺序靠前者），其余改名，总代价最小。
 * 新名字为 "<基名>-<n>.<扩展名>"，取不与任何现存/已分配键冲突的最小 n。
 */
export function planRenames(
  modules: IndexedModule[],
  conflicts: ConflictGroup[],
  extensions: string[],
): RenameAction[] {
  const usedKeys = new Set(modules.map((m) => m.key));
  const byKey = new Map<string, IndexedModule[]>();
  for (const m of modules) {
    const group = byKey.get(m.key);
    if (group) group.push(m);
    else byKey.set(m.key, [m]);
  }

  const renames: RenameAction[] = [];
  for (const conflict of conflicts) {
    const group = byKey.get(conflict.key)!;
    // 保留代价最高者；稳定排序保证并列时确定性。
    const sorted = [...group].sort((a, b) => b.renameCost - a.renameCost);
    const toRename = sorted.slice(1);
    for (const m of toRename) {
      const next = freshName(m.actual, usedKeys, extensions);
      usedKeys.add(nextKeyOf(next, extensions));
      renames.push({ from: m.actual, to: next, cost: m.renameCost });
    }
  }
  return renames;
}

function nextKeyOf(actual: string, extensions: string[]): string {
  let out = actual.toLowerCase();
  for (;;) {
    const hit = extensions.find(
      (ext) => out.endsWith(ext.toLowerCase()) && out.length > ext.length,
    );
    if (!hit) return out;
    out = out.slice(0, out.length - hit.length);
  }
}

function freshName(actual: string, usedKeys: Set<string>, extensions: string[]): string {
  const dir = dirName(actual);
  const base = baseName(actual);
  const dot = base.lastIndexOf(".");
  const stem = dot > 0 ? base.slice(0, dot) : base;
  const ext = dot > 0 ? base.slice(dot) : "";
  for (let n = 2; ; n++) {
    const candidateBase = `${stem}-${n}${ext}`;
    const candidate = dir === "" ? candidateBase : `${dir}/${candidateBase}`;
    if (!usedKeys.has(nextKeyOf(candidate, extensions))) return candidate;
  }
}

function updateReferences(
  input: AnalyzerInput,
  originalModules: IndexedModule[],
  finalModules: IndexedModule[],
  renamedTo: Map<string, string>,
  aliasRules: ReturnType<typeof compileAliases>,
  extensions: string[],
): { updatedReferences: ReferenceUpdate[]; badReferences: BadReference[] } {
  const updatedReferences: ReferenceUpdate[] = [];
  const badReferences: BadReference[] = [];

  for (const ref of input.references ?? []) {
    const fromActual =
      originalModules.find((m) => m.inputPath === ref.from)?.actual ??
      // importer 本身可能不在模块清单中，按同样规则规范化。
      indexModules([{ path: ref.from }], aliasRules, extensions)[0].actual;
    const fromDir = dirName(fromActual);

    const resolution = resolveSpecifier(
      ref.specifier,
      fromDir,
      originalModules,
      aliasRules,
      extensions,
    );

    if (resolution.kind === "external") continue;
    if (resolution.kind === "unresolved") {
      badReferences.push({ from: ref.from, specifier: ref.specifier, reason: "unresolved" });
      continue;
    }
    if (resolution.kind === "ambiguous") {
      badReferences.push({
        from: ref.from,
        specifier: ref.specifier,
        reason: "ambiguous",
        candidates: resolution.candidates.map((c) => c.actual),
      });
      continue;
    }

    const target = resolution.module;
    const newActual = renamedTo.get(target.actual);
    if (newActual === undefined) continue; // 目标未改名，引用保持不变。

    const finalTarget = finalModules.find((m) => m.actual === newActual)!;
    const newSpecifier = renderSpecifier(
      finalTarget.actual,
      fromDir,
      ref.specifier,
      aliasRules,
      extensions,
    );
    updatedReferences.push({
      from: ref.from,
      oldSpecifier: ref.specifier,
      newSpecifier,
      resolvesTo: finalTarget.actual,
    });
  }

  return { updatedReferences, badReferences };
}
