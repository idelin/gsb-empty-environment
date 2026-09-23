/**
 * 计算定义注册表。
 * 计算函数绝不序列化：快照只保存 (defId, defVersion) 与依赖边，
 * 恢复时按标识+版本从注册表找回函数本体。
 */
export interface ComputationDef {
  readonly id: string;
  readonly version: number;
  /**
   * monotonic=true 的定义允许一条指向自身的反馈边（单调环），
   * 求值时拿到自己上一轮的提交值（首轮为 initial）。
   */
  readonly monotonic?: boolean;
  readonly initial?: unknown;
  readonly compute: (deps: readonly unknown[]) => unknown;
}

export class Registry {
  private readonly defs = new Map<string, ComputationDef>();

  private static key(id: string, version: number): string {
    return `${id}@${version}`;
  }

  register(def: ComputationDef): this {
    const key = Registry.key(def.id, def.version);
    if (this.defs.has(key)) {
      throw new Error(`definition already registered: ${key}`);
    }
    this.defs.set(key, def);
    return this;
  }

  get(id: string, version: number): ComputationDef | undefined {
    return this.defs.get(Registry.key(id, version));
  }

  /** 该 id 是否存在（任意版本），用于区分“未知定义”与“版本不符”。 */
  hasId(id: string): boolean {
    for (const def of this.defs.values()) {
      if (def.id === id) return true;
    }
    return false;
  }

  versionsOf(id: string): number[] {
    const out: number[] = [];
    for (const def of this.defs.values()) {
      if (def.id === id) out.push(def.version);
    }
    return out.sort((a, b) => a - b);
  }
}
