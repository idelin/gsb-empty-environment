import type { ModuleSource } from "./types.ts";
import { link, type ModuleInstance } from "./linker.ts";

/** 存活决策快照：某一时刻整批模块的不可变视图。
 * 热替换后旧快照仍服务旧版本，进行中的决策不受影响。 */
export class Snapshot {
  readonly seq: number;
  /** 键为模块键 "name@version"。 */
  readonly modules: ReadonlyMap<string, ModuleInstance>;
  /** 全量版本向量（所有模块键排序连接）。 */
  readonly vector: string;
  private readonly byName: Map<string, ModuleInstance>;

  constructor(modules: Map<string, ModuleInstance>, seq: number) {
    this.modules = new Map(modules);
    this.seq = seq;
    this.vector = [...modules.keys()].sort().join(",");
    this.byName = new Map();
    for (const inst of modules.values()) this.byName.set(inst.name, inst);
  }

  get(name: string): ModuleInstance | undefined {
    return this.byName.get(name);
  }
}

export class ModuleRegistry {
  private current: Snapshot | null = null;
  private seq = 0;

  /** 热替换：先整批链接（签名检查 + 环检测 + 构建），全部成功才原子换版；
   * 任何失败抛 LinkFailure，当前版本保持不变（不允许部分模块换版）。 */
  hotReplace(sources: ModuleSource[]): Snapshot {
    const linked = link(sources);
    const snap = new Snapshot(linked, ++this.seq);
    this.current = snap;
    return snap;
  }

  /** 取当前存活决策快照。 */
  snapshot(): Snapshot {
    if (!this.current) throw new Error("注册表为空，尚未链接任何模块");
    return this.current;
  }
}
