import { evaluateCond, validateCond, type Attrs, type Cond } from "./policy.ts";
import type { Snapshot } from "./registry.ts";

export class RuleError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "RuleError";
  }
}

/** 规则声明负载：模块以 signature "rule" 的导出发布。 */
export interface RulePayload {
  type: "rule";
  effect: "allow" | "deny";
  condition: Cond;
}

export interface Rule {
  /** 全限定名 "module:name"，命名空间隔离，同名规则互不覆盖。 */
  readonly qualifiedName: string;
  readonly module: string;
  readonly name: string;
  readonly effect: "allow" | "deny";
  readonly condition: Cond;
}

export class RuleBook {
  private readonly rules = new Map<string, Rule>();

  private constructor() {}

  static fromSnapshot(snapshot: Snapshot): RuleBook {
    const book = new RuleBook();
    for (const inst of snapshot.modules.values()) {
      for (const [exportName, value] of inst.exports) {
        if (inst.kinds.get(exportName) === undefined) continue;
        const payload = value as RulePayload | null | undefined;
        if (!payload || typeof payload !== "object" || payload.type !== "rule") continue;
        validateCond(payload.condition, `${inst.key}:${exportName}`);
        if (payload.effect !== "allow" && payload.effect !== "deny") {
          throw new RuleError(`${inst.key}:${exportName} 的 effect 必须是 allow 或 deny`);
        }
        const qualifiedName = `${inst.name}:${exportName}`;
        if (book.rules.has(qualifiedName)) {
          throw new RuleError(`规则冲突: ${qualifiedName} 已存在（同名规则必须命名空间隔离）`);
        }
        book.rules.set(qualifiedName, {
          qualifiedName,
          module: inst.name,
          name: exportName,
          effect: payload.effect,
          condition: payload.condition,
        });
      }
    }
    return book;
  }

  get(qualifiedName: string): Rule | undefined {
    return this.rules.get(qualifiedName);
  }

  list(): Rule[] {
    return [...this.rules.values()];
  }

  /** 命中返回 effect，未命中返回 null。 */
  evaluate(qualifiedName: string, attrs: Attrs): "allow" | "deny" | null {
    const rule = this.rules.get(qualifiedName);
    if (!rule) throw new RuleError(`规则不存在: ${qualifiedName}`);
    return evaluateCond(rule.condition, attrs) ? rule.effect : null;
  }
}
