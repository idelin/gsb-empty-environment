/**
 * 测试基建：
 * - ReferenceModel：完整深拷贝状态历史，作为 Store 的独立参考实现（无任何结构共享）。
 * - runOpLog：把纯数据的操作日志解释执行在 Store 上并记录事件流，可原样回放。
 * - mulberry32：确定性伪随机数，用于可复现的随机对照测试。
 */
import assert from "node:assert/strict";
import {
  Store,
  Snapshot,
  Transaction,
  type JsonObject,
  type JsonValue,
  type Path,
} from "../src/store.ts";

/* ---------------- 独立参考：完整深拷贝历史 ---------------- */

export class ReferenceModel {
  private history = new Map<number, JsonObject>();
  private counter = 0;
  private head = 0;
  private workings = new Map<string, JsonObject>();

  constructor(initial: JsonObject) {
    this.counter = 1;
    this.head = 1;
    this.history.set(1, structuredClone(initial));
  }

  get headVersion(): number {
    return this.head;
  }

  begin(tx: string, from?: number): void {
    const version = from ?? this.head;
    const base = this.history.get(version);
    assert.ok(base, `参考模型不存在版本 ${version}`);
    this.workings.set(tx, structuredClone(base));
  }

  set(tx: string, path: Path, value: JsonValue): void {
    setMutable(this.mustWorking(tx), path, structuredClone(value));
  }

  delete(tx: string, path: Path): void {
    deleteMutable(this.mustWorking(tx), path);
  }

  commit(tx: string): number {
    const version = ++this.counter;
    this.history.set(version, this.mustWorking(tx));
    this.workings.delete(tx);
    this.head = version;
    return version;
  }

  abort(tx: string): void {
    this.workings.delete(tx);
  }

  stateAt(version: number): JsonObject {
    const state = this.history.get(version);
    assert.ok(state, `参考模型不存在版本 ${version}`);
    return state;
  }

  get headState(): JsonObject {
    return this.stateAt(this.head);
  }

  /** 读取事务工作区中的值（生成器用来保证随机操作合法）。 */
  get(tx: string, path: Path): JsonValue | undefined {
    let node: any = this.mustWorking(tx);
    for (const key of path) {
      if (typeof node !== "object" || node === null) return undefined;
      node = node[key];
    }
    return node;
  }

  private mustWorking(tx: string): JsonObject {
    const working = this.workings.get(tx);
    assert.ok(working, `参考模型不存在事务 ${tx}`);
    return working;
  }
}

function setMutable(root: JsonObject, path: Path, value: JsonValue): void {
  let node: any = root;
  for (let i = 0; i < path.length - 1; i += 1) {
    const key = path[i];
    if (node[key] === undefined) node[key] = typeof path[i + 1] === "number" ? [] : {};
    node = node[key];
  }
  node[path[path.length - 1]] = value;
}

function deleteMutable(root: JsonObject, path: Path): void {
  let node: any = root;
  for (let i = 0; i < path.length - 1; i += 1) node = node[path[i]];
  const last = path[path.length - 1];
  if (Array.isArray(node)) node.splice(last as number, 1);
  else delete node[last];
}

/* ---------------- 操作日志（纯数据，可回放） ---------------- */

export type ListenerSpec = {
  /** 每次回调都抛错。 */
  throwOnCall?: boolean;
  /** 首次收到通知时重入发起一个新事务并提交。 */
  recommitOnce?: { path: (string | number)[]; value: JsonValue };
  /** 首次收到通知时取消这些订阅。 */
  unsubscribeOnFirst?: string[];
};

export type Op =
  | { op: "begin"; tx: string; from?: number }
  | { op: "set"; tx: string; path: (string | number)[]; value: JsonValue }
  | { op: "delete"; tx: string; path: (string | number)[] }
  | { op: "commit"; tx: string }
  | { op: "abort"; tx: string }
  | { op: "subscribe"; id: string; spec?: ListenerSpec }
  | { op: "unsubscribe"; id: string }
  | { op: "snapshot"; id: string }
  | { op: "checkout"; id: string; version: number }
  | { op: "release"; id: string };

/**
 * 在 Store 上解释执行操作日志，把全部可观察事件追加到 record。
 * 传入 ReferenceModel 时同步镜像数据操作，并逐步断言两边状态完全一致。
 */
export function runOpLog(store: Store, ops: Op[], record: string[], ref?: ReferenceModel): void {
  const txs = new Map<string, Transaction>();
  const unsubscribes = new Map<string, () => void>();
  const snapshots = new Map<string, Snapshot>();
  const calledOnce = new Set<string>();

  for (const op of ops) {
    switch (op.op) {
      case "begin":
        txs.set(op.tx, store.begin(op.from !== undefined ? { from: op.from } : {}));
        ref?.begin(op.tx, op.from);
        break;
      case "set":
        txs.get(op.tx)!.set(op.path, op.value);
        ref?.set(op.tx, op.path, op.value);
        break;
      case "delete":
        txs.get(op.tx)!.delete(op.path);
        ref?.delete(op.tx, op.path);
        break;
      case "commit": {
        // 先镜像到参考模型：store.commit 会同步触发通知，通知里要对照参考状态。
        const expected = ref?.commit(op.tx);
        const version = txs.get(op.tx)!.commit();
        txs.delete(op.tx);
        record.push(`commit:${op.tx}:v${version}`);
        if (ref) {
          assert.equal(version, expected, "版本号与参考模型不一致");
          const probe = store.checkout(version);
          assert.deepEqual(probe.state, ref.stateAt(version));
          probe.release();
        }
        break;
      }
      case "abort":
        txs.get(op.tx)!.abort();
        txs.delete(op.tx);
        ref?.abort(op.tx);
        record.push(`abort:${op.tx}`);
        break;
      case "subscribe": {
        const spec = op.spec ?? {};
        const off = store.subscribe((state, version) => {
          record.push(`notify:${op.id}:v${version}:${JSON.stringify(state)}`);
          if (ref) assert.deepEqual(state, ref.stateAt(version), "通知状态与参考模型不一致");
          if (spec.throwOnCall) throw new Error(`listener ${op.id} boom`);
          if (!calledOnce.has(op.id)) {
            calledOnce.add(op.id);
            for (const target of spec.unsubscribeOnFirst ?? []) {
              unsubscribes.get(target)?.();
              record.push(`unsub-by:${op.id}:${target}`);
            }
            if (spec.recommitOnce) {
              // 同样先镜像参考模型，再提交 store（其通知会同步对照）。
              ref?.begin(`reentrant-${op.id}`);
              ref?.set(`reentrant-${op.id}`, spec.recommitOnce.path, spec.recommitOnce.value);
              const expected = ref?.commit(`reentrant-${op.id}`);
              const tx = store.begin();
              tx.set(spec.recommitOnce.path, spec.recommitOnce.value);
              const v = tx.commit();
              record.push(`recommit-by:${op.id}:v${v}`);
              if (ref) assert.equal(v, expected);
            }
          }
        });
        unsubscribes.set(op.id, off);
        break;
      }
      case "unsubscribe":
        unsubscribes.get(op.id)?.();
        record.push(`unsub:${op.id}`);
        break;
      case "snapshot": {
        const snap = store.snapshot();
        snapshots.set(op.id, snap);
        record.push(`snapshot:${op.id}:v${snap.version}`);
        break;
      }
      case "checkout": {
        const snap = store.checkout(op.version);
        snapshots.set(op.id, snap);
        record.push(`checkout:${op.id}:v${snap.version}:${JSON.stringify(snap.state)}`);
        if (ref) assert.deepEqual(snap.state, ref.stateAt(op.version));
        break;
      }
      case "release":
        snapshots.get(op.id)!.release();
        record.push(`release:${op.id}`);
        break;
    }
  }
  record.push(`ledger:${store.ledger.retainedNodes}:[${store.ledger.versions.join(",")}]`);
}

/* ---------------- 确定性随机数 ---------------- */

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
