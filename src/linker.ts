import type {
  ExportDecl,
  ExportKind,
  ModuleSource,
  NamespaceView,
} from "./types.ts";
import { LinkError, LinkFailure } from "./errors.ts";
import { shortestCycle, topoSort, type Graph } from "./graph.ts";

export interface ModuleInstance {
  readonly name: string;
  readonly version: string;
  /** 模块键 "name@version"。 */
  readonly key: string;
  readonly exports: ReadonlyMap<string, unknown>;
  readonly kinds: ReadonlyMap<string, ExportKind>;
  /** 直接依赖模块键。 */
  readonly deps: readonly string[];
  /** 完整依赖版本向量（含自身，排序后逗号连接），用作缓存隔离键。 */
  readonly versionVector: string;
}

export const keyOf = (name: string, version: string): string => `${name}@${version}`;

interface Shell {
  src: ModuleSource;
  key: string;
  exports: Map<string, unknown>;
  kinds: Map<string, ExportKind>;
  localNames: Set<string>;
  /** 本地名 -> 导入绑定。 */
  importBindings: Map<string, { targetKey: string; exportName: string }>;
}

function exportIndex(src: ModuleSource): Map<string, ExportDecl> {
  const idx = new Map<string, ExportDecl>();
  for (const e of src.exports) idx.set(e.name, e);
  return idx;
}

/** 分别构建声明图与求值图（假定签名检查已通过）。 */
export function buildDepGraphs(sources: ModuleSource[]): { declGraph: Graph; evalGraph: Graph } {
  const byName = new Map(sources.map((s) => [s.name, s]));
  const declGraph: Graph = new Map();
  const evalGraph: Graph = new Map();
  for (const src of sources) {
    const srcKey = keyOf(src.name, src.version);
    if (!declGraph.has(srcKey)) declGraph.set(srcKey, new Set());
    if (!evalGraph.has(srcKey)) evalGraph.set(srcKey, new Set());
    for (const imp of src.imports) {
      const target = byName.get(imp.module);
      if (!target || target.version !== imp.version) continue;
      const targetKey = keyOf(target.name, target.version);
      const idx = exportIndex(target);
      for (const n of imp.names) {
        const exp = idx.get(n.name);
        if (!exp) continue;
        const graph = exp.kind === "decl" ? declGraph : evalGraph;
        const edges = new Set(graph.get(srcKey) ?? []);
        edges.add(targetKey);
        graph.set(srcKey, edges);
      }
    }
  }
  return { declGraph, evalGraph };
}

function checkSignatures(sources: ModuleSource[], byName: Map<string, ModuleSource>): LinkError[] {
  const errors: LinkError[] = [];
  for (const src of sources) {
    const srcKey = keyOf(src.name, src.version);
    for (const imp of src.imports) {
      const wantKey = keyOf(imp.module, imp.version);
      const target = byName.get(imp.module);
      if (!target) {
        errors.push(
          new LinkError(
            "MODULE_NOT_FOUND",
            `模块未找到: ${srcKey} 依赖 ${wantKey}（导入语句位于 ${imp.loc}）`,
            [srcKey, wantKey],
            imp.loc,
          ),
        );
        continue;
      }
      const targetKey = keyOf(target.name, target.version);
      if (target.version !== imp.version) {
        errors.push(
          new LinkError(
            "VERSION_MISMATCH",
            `版本不匹配: ${srcKey} 需要 ${wantKey}，但批次中 ${imp.module} 的版本为 ${target.version}（导入语句位于 ${imp.loc}）`,
            [srcKey, wantKey],
            imp.loc,
          ),
        );
        continue;
      }
      const idx = exportIndex(target);
      for (const n of imp.names) {
        const exp = idx.get(n.name);
        if (!exp) {
          errors.push(
            new LinkError(
              "MISSING_EXPORT",
              `缺失导出: 模块 ${targetKey} 不存在导出 "${n.name}"（导入语句位于 ${imp.loc}）`,
              [srcKey, targetKey],
              imp.loc,
            ),
          );
          continue;
        }
        if (n.signature !== undefined && n.signature !== exp.signature) {
          errors.push(
            new LinkError(
              "SIGNATURE_MISMATCH",
              `签名不匹配: ${srcKey} 期望 ${targetKey} 的 "${n.name}" 签名为 "${n.signature}"，实际为 "${exp.signature}"（导入语句位于 ${imp.loc}）`,
              [srcKey, targetKey],
              imp.loc,
            ),
          );
        }
      }
    }
  }
  return errors;
}

function computeVersionVectors(keys: string[], combined: Graph): Map<string, string> {
  const vectors = new Map<string, string>();
  const visit = (k: string, acc: Set<string>) => {
    for (const d of combined.get(k) ?? []) {
      if (!acc.has(d)) {
        acc.add(d);
        visit(d, acc);
      }
    }
  };
  for (const k of keys) {
    const acc = new Set<string>([k]);
    visit(k, acc);
    vectors.set(k, [...acc].sort().join(","));
  }
  return vectors;
}

/**
 * 批量链接：签名检查全部通过后整批构建；任何失败抛 LinkFailure，
 * 不产生任何部分结果（调用方据此保证不换版）。
 */
export function link(sources: ModuleSource[]): Map<string, ModuleInstance> {
  // 第 0 阶段：重名检查
  const byName = new Map<string, ModuleSource>();
  const dupErrors: LinkError[] = [];
  for (const src of sources) {
    const prev = byName.get(src.name);
    if (prev) {
      dupErrors.push(
        new LinkError(
          "DUPLICATE_MODULE",
          `重复模块: ${src.name} 同时出现版本 ${prev.version} 与 ${src.version}`,
          [keyOf(src.name, src.version)],
        ),
      );
    } else {
      byName.set(src.name, src);
    }
  }
  if (dupErrors.length > 0) throw new LinkFailure(dupErrors);

  // 第 1 阶段：导入导出签名检查（收集全部错误后才进入链接）
  const sigErrors = checkSignatures(sources, byName);
  if (sigErrors.length > 0) throw new LinkFailure(sigErrors);

  // 第 2 阶段：声明环与求值环分别建图；仅求值环报错，输出最短来源链
  const { declGraph, evalGraph } = buildDepGraphs(sources);
  const evalCycle = shortestCycle(evalGraph);
  if (evalCycle) {
    throw new LinkFailure([
      new LinkError(
        "EVAL_CYCLE",
        `求值环（禁止）: ${evalCycle.join(" -> ")}；仅无求值副作用的声明引用允许成环`,
        evalCycle,
      ),
    ]);
  }

  // 第 3 阶段：整批构建
  const keys = sources.map((s) => keyOf(s.name, s.version));
  const order = topoSort(keys, evalGraph);

  const combined: Graph = new Map();
  for (const k of keys) {
    combined.set(k, new Set([...(declGraph.get(k) ?? []), ...(evalGraph.get(k) ?? [])]));
  }
  const vectors = computeVersionVectors(keys, combined);

  // 先建壳并解析全部 decl 导出（声明不求值，声明环安全）
  const shells = new Map<string, Shell>();
  for (const src of sources) {
    const key = keyOf(src.name, src.version);
    const exports = new Map<string, unknown>();
    const kinds = new Map<string, ExportKind>();
    for (const exp of src.exports) {
      kinds.set(exp.name, exp.kind);
      if (exp.kind === "decl") exports.set(exp.name, exp.decl);
    }
    const importBindings = new Map<string, { targetKey: string; exportName: string }>();
    for (const imp of src.imports) {
      const targetKey = keyOf(imp.module, imp.version);
      for (const n of imp.names) {
        importBindings.set(n.as ?? n.name, { targetKey, exportName: n.name });
      }
    }
    shells.set(key, {
      src,
      key,
      exports,
      kinds,
      localNames: new Set(src.exports.map((e) => e.name)),
      importBindings,
    });
  }

  const makeGet = (shell: Shell) => (localName: string): unknown => {
    if (shell.localNames.has(localName)) {
      // 本地导出遮蔽同名导入
      if (shell.exports.has(localName)) return shell.exports.get(localName);
      throw new Error(`本地导出 "${localName}" 尚未解析（注意模块内求值顺序）`);
    }
    const binding = shell.importBindings.get(localName);
    if (!binding) {
      throw new Error(`命名空间 ${shell.key} 中不存在名称 "${localName}"`);
    }
    const dep = shells.get(binding.targetKey);
    if (!dep || !dep.exports.has(binding.exportName)) {
      throw new Error(`依赖 ${binding.targetKey} 的导出 "${binding.exportName}" 尚未解析`);
    }
    return dep.exports.get(binding.exportName);
  };

  // 按求值图拓扑序解析 value 导出并执行 init
  const instances = new Map<string, ModuleInstance>();
  for (const key of order) {
    const shell = shells.get(key) as Shell;
    const get = makeGet(shell);
    for (const exp of shell.src.exports) {
      if (exp.kind !== "value") continue;
      try {
        shell.exports.set(
          exp.name,
          (exp.make as NonNullable<typeof exp.make>)({
            module: shell.src.name,
            version: shell.src.version,
            get,
          }),
        );
      } catch (cause) {
        throw new LinkFailure([
          new LinkError(
            "INIT_FAILURE",
            `模块 ${key} 的导出 "${exp.name}" 求值失败: ${(cause as Error).message}`,
            [key],
          ),
        ]);
      }
    }
    if (shell.src.init) {
      const view: NamespaceView = {
        has: (n) => shell.localNames.has(n) || shell.importBindings.has(n),
        get,
        names: () => [...shell.localNames, ...shell.importBindings.keys()],
      };
      try {
        shell.src.init(view);
      } catch (cause) {
        throw new LinkFailure([
          new LinkError(
            "INIT_FAILURE",
            `模块 ${key} 初始化失败: ${(cause as Error).message}`,
            [key],
          ),
        ]);
      }
    }
    instances.set(key, {
      name: shell.src.name,
      version: shell.src.version,
      key,
      exports: new Map(shell.exports),
      kinds: new Map(shell.kinds),
      deps: [...(combined.get(key) ?? [])],
      versionVector: vectors.get(key) as string,
    });
  }
  return instances;
}
