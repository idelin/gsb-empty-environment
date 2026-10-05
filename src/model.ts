// 独立向量前缀参考模型: 只用集合与查表实现, 用于交叉校验主实现。
import type { Message, MessageId } from "./types.ts";
import { idKey, prefixKey } from "./types.ts";

export class VectorPrefixModel {
  private readonly ids = new Set<string>();
  private readonly prefix = new Map<string, number>();
  private readonly pending: Message[] = []; // 已到达但未满足交付条件的消息

  canDeliver(msg: Message): boolean {
    const pkey = prefixKey(msg.id.node, msg.id.epoch);
    if (this.ids.has(idKey(msg.id))) return false;
    if (msg.id.counter !== (this.prefix.get(pkey) ?? 0) + 1) return false;
    for (const [dep, need] of Object.entries(msg.deps)) {
      if ((this.prefix.get(dep) ?? 0) < need) return false;
    }
    return true;
  }

  deliver(msg: Message): void {
    if (!this.canDeliver(msg)) throw new Error(`model: not deliverable ${idKey(msg.id)}`);
    this.ids.add(idKey(msg.id));
    this.prefix.set(prefixKey(msg.id.node, msg.id.epoch), msg.id.counter);
  }

  // 新到达一批消息后求交付不动点(暂存的消息会一直保留到可交付), 返回本次新交付。
  closure(arrived: Message[]): Message[] {
    this.pending.push(...arrived);
    const out: Message[] = [];
    let progressed = true;
    while (progressed) {
      progressed = false;
      for (let i = 0; i < this.pending.length; i++) {
        if (this.ids.has(idKey(this.pending[i].id))) {
          this.pending.splice(i, 1); // 重复副本, 幂等吸收
          i -= 1;
          continue;
        }
        if (this.canDeliver(this.pending[i])) {
          this.deliver(this.pending[i]);
          out.push(this.pending[i]);
          this.pending.splice(i, 1);
          i -= 1;
          progressed = true;
        }
      }
    }
    return out;
  }

  has(id: MessageId): boolean {
    return this.ids.has(idKey(id));
  }

  deliveredSet(): Set<string> {
    return new Set(this.ids);
  }
}

// 交付顺序不变量: 无重复、每条依赖在前、本节点序号无缺口误推进。
export function orderViolations(order: Message[]): string[] {
  const violations: string[] = [];
  const seen = new Set<string>();
  const prefix = new Map<string, number>();
  order.forEach((msg, index) => {
    const key = idKey(msg.id);
    if (seen.has(key)) violations.push(`#${index} duplicate ${key}`);
    seen.add(key);
    const pkey = prefixKey(msg.id.node, msg.id.epoch);
    const upto = prefix.get(pkey) ?? 0;
    if (msg.id.counter !== upto + 1) {
      violations.push(`#${index} gap: ${key} follows ${pkey}<=${upto}`);
    }
    for (const [dep, need] of Object.entries(msg.deps)) {
      if ((prefix.get(dep) ?? 0) < need) {
        violations.push(`#${index} unmet dep ${dep}<=${need} at ${key}`);
      }
    }
    prefix.set(pkey, msg.id.counter);
  });
  return violations;
}
