import { Compiler, evalNode, evalPred, type Node } from './dag.ts';
import type { Expr } from './expr.ts';
import { triAnd, triNot, triOr, type Tri } from './logic.ts';
import type { AttrStore } from './store.ts';

export type Decision =
  | {
      status: 'decided';
      value: Tri;
      explanation: Record<string, number>;
      fromCache: boolean;
      attempts: number;
    }
  | { status: 'conflict'; attempts: number }
  | { status: 'aborted' }
  | { status: 'error'; message: string };

class VersionConflict extends Error {}

type CacheEntry = {
  value: Tri;
  explanation: Record<string, number>;
  reads: Record<string, number>;
  rulesVersion: number;
};

function isAbort(err: unknown): boolean {
  return err instanceof Error && err.name === 'AbortError';
}

function abortError(): Error {
  const err = new Error('evaluation aborted');
  err.name = 'AbortError';
  return err;
}

/**
 * 策略引擎：编译为共享 DAG，带短路求值、读取依赖记录、
 * 版本一致性校验（混版本时重试或报冲突，绝不混版本放行）、
 * 按主体隔离的缓存（属性/规则热更新精确失效）、最小解释切片。
 */
export class PolicyEngine {
  private compiler = new Compiler();
  private root: Node;
  private rulesVersion = 0;
  private cache = new Map<string, CacheEntry>();
  private store: AttrStore;

  constructor(store: AttrStore, expr: Expr) {
    this.store = store;
    this.root = this.compiler.compile(expr);
  }

  get policyRoot(): Node {
    return this.root;
  }

  /** 规则热更新：全部缓存条目因 rulesVersion 过期而失效。 */
  setPolicy(expr: Expr): void {
    this.root = this.compiler.compile(expr);
    this.rulesVersion++;
  }

  get cacheSize(): number {
    return this.cache.size;
  }

  async decide(
    subject: string,
    opts: { signal?: AbortSignal; maxRetries?: number } = {},
  ): Promise<Decision> {
    const cached = this.cache.get(subject);
    if (cached && this.isValid(subject, cached)) {
      return {
        status: 'decided',
        value: cached.value,
        explanation: cached.explanation,
        fromCache: true,
        attempts: 0,
      };
    }
    const maxRetries = opts.maxRetries ?? 3;
    let attempts = 0;
    while (attempts < maxRetries) {
      attempts++;
      try {
        const { value, reads } = await this.evalOnce(subject, opts.signal);
        const explanation = this.minimalExplanation(value, reads);
        const entry: CacheEntry = {
          value,
          explanation,
          reads: Object.fromEntries(
            [...reads].map(([attr, r]) => [attr, r.version]),
          ),
          rulesVersion: this.rulesVersion,
        };
        this.cache.set(subject, entry);
        return { status: 'decided', value, explanation, fromCache: false, attempts };
      } catch (err) {
        if (err instanceof VersionConflict) continue;
        if (isAbort(err)) return { status: 'aborted' };
        return { status: 'error', message: String(err) };
      }
    }
    return { status: 'conflict', attempts };
  }

  private isValid(subject: string, entry: CacheEntry): boolean {
    if (entry.rulesVersion !== this.rulesVersion) return false;
    return Object.entries(entry.reads).every(
      ([attr, version]) => this.store.versionOf(subject, attr) === version,
    );
  }

  private async evalOnce(
    subject: string,
    signal?: AbortSignal,
  ): Promise<{ value: Tri; reads: Map<string, { version: number; value: number | undefined }> }> {
    const reads = new Map<string, { version: number; value: number | undefined }>();
    const memo = new Map<number, Promise<Tri>>();

    const evalN = async (node: Node): Promise<Tri> => {
      const hit = memo.get(node.id);
      if (hit) return hit;
      const promise = (async (): Promise<Tri> => {
        switch (node.kind) {
          case 'lit':
            return node.value;
          case 'cond': {
            if (signal?.aborted) throw abortError();
            const { value, version } = await this.store.read(subject, node.attr);
            const prev = reads.get(node.attr);
            if (prev && prev.version !== version) throw new VersionConflict();
            reads.set(node.attr, { version, value });
            return evalPred(node.pred, value);
          }
          case 'not':
            return triNot(await evalN(node.arg));
          case 'and': {
            let acc: Tri = 'true';
            for (const child of node.args) {
              acc = triAnd(acc, await evalN(child));
              if (acc === 'false') break; // 短路：后续子节点的属性不被读取
            }
            return acc;
          }
          case 'or': {
            let acc: Tri = 'false';
            for (const child of node.args) {
              acc = triOr(acc, await evalN(child));
              if (acc === 'true') break;
            }
            return acc;
          }
        }
      })();
      memo.set(node.id, promise);
      return promise;
    };

    const value = await evalN(this.root);
    // 请求内一致性校验：任何已读属性的版本在求值期间变化 => 冲突，重试。
    for (const [attr, read] of reads) {
      if (this.store.versionOf(subject, attr) !== read.version) {
        throw new VersionConflict();
      }
    }
    return { value, reads };
  }

  /**
   * 最小解释切片：从实际读取的属性出发，逐个试删（置为 unknown），
   * 结论不变则永久移除；剩下的即为支撑结论的最小属性集合。
   */
  private minimalExplanation(
    value: Tri,
    reads: Map<string, { version: number; value: number | undefined }>,
  ): Record<string, number> {
    const removed = new Set<string>();
    const assignment = new Map([...reads].map(([attr, r]) => [attr, r.value]));
    for (const attr of reads.keys()) {
      removed.add(attr);
      const v = evalNode(this.root, (a) =>
        removed.has(a) ? undefined : assignment.get(a),
      );
      if (v !== value) removed.delete(attr);
    }
    const out: Record<string, number> = {};
    for (const [attr, r] of reads) {
      if (!removed.has(attr) && r.value !== undefined) out[attr] = r.value;
    }
    return out;
  }

  /**
   * 部分求值：已知属性折叠为常量（缺失 => unknown 常量），
   * 返回针对剩余未知属性的残差条件 DAG。
   */
  partial(known: Record<string, number | undefined>): { root: Node; remaining: string[] } {
    const knownMap = new Map(Object.entries(known));
    const rec = (node: Node): Node => {
      switch (node.kind) {
        case 'lit':
          return node;
        case 'cond': {
          if (!knownMap.has(node.attr)) return node;
          const v = knownMap.get(node.attr);
          return this.compiler.lit(v === undefined ? 'unknown' : evalPred(node.pred, v));
        }
        case 'not':
          return this.compiler.neg(rec(node.arg));
        case 'and':
        case 'or':
          return this.compiler.join(node.kind, node.args.map(rec));
      }
    };
    const root = rec(this.root);
    const remaining = new Set<string>();
    const walk = (node: Node): void => {
      if (node.kind === 'cond') remaining.add(node.attr);
      else if (node.kind === 'not') walk(node.arg);
      else if (node.kind === 'and' || node.kind === 'or') node.args.forEach(walk);
    };
    walk(root);
    return { root, remaining: [...remaining].sort() };
  }
}
