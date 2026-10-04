import { appendFileSync, existsSync, mkdirSync, readFileSync, unlinkSync } from 'node:fs';
import { join } from 'node:path';
import type { JsonValue } from './ast.ts';
import type { CompiledSegment, FilterPlan, Program } from './compiler.ts';
import { needsContainerEnd } from './compiler.ts';
import type { Event, NodeKind } from './events.ts';
import { walk } from './events.ts';
import { evaluateFrom, sliceIndices } from './interpreter.ts';
import type { Match } from './interpreter.ts';
import { canonical, isPrefix } from './path.ts';
import type { Path } from './path.ts';
import { BufferOverflowError, CancelledError } from './errors.ts';
import type { BudgetTracker } from './budget.ts';

export interface StreamOptions {
  budget?: BudgetTracker;
  /** 单个暂存区（收集器/延期队列）的最大在内存条目数，超出后溢写或报错。 */
  maxBuffered?: number;
  /** 溢写目录；缺省时超出 maxBuffered 抛 BufferOverflowError。 */
  spillDir?: string;
  /** 取消信号；触发后清理全部临时文件并抛 CancelledError。 */
  signal?: AbortSignal;
  /** 结果一旦确定立即回调（增量输出），第二参数为已消费事件数。 */
  onMatch?: (m: Match, eventsConsumed: number) => void;
}

export interface StreamResult {
  matches: Match[];
  /** 是否发生过溢写。 */
  spilled: boolean;
  eventsConsumed: number;
}

interface Collector {
  canon: string;
  path: Path;
  seg: number;
  segment: CompiledSegment;
  items: { path: Path; value: JsonValue }[];
  spillFiles: string[];
}

interface DeferredItem {
  path: Path;
  value: JsonValue;
  seg: number;
  predFilter?: FilterPlan;
}

const DEFAULT_MAX_BUFFERED = 1024;

/**
 * 流式执行引擎：消费 enter/exit 事件流，结果一旦可判定立即输出。
 * - 非负下标/切片/通配/字段/递归：纯状态机，O(查询) 附加状态；
 * - 负下标、负切片、反向步长：收集该容器子节点直到容器闭合（尾部索引此前不可判定）；
 * - 引用 $ 的谓词：需要全局信息，有界暂存并延期到根闭合；
 * - 暂存超出 maxBuffered：配置了 spillDir 则溢写临时文件，否则抛 BufferOverflowError。
 */
export function runStream(prog: Program, events: Iterable<Event>, opts: StreamOptions = {}): StreamResult {
  return new StreamEngine(prog, opts).run(events);
}

/** 便捷入口：直接对 JSON 树流式执行。 */
export function streamQuery(root: JsonValue, prog: Program, opts: StreamOptions = {}): StreamResult {
  return runStream(prog, walk(root), opts);
}

class StreamEngine {
  private readonly prog: Program;
  private readonly opts: StreamOptions;
  private readonly maxBuffered: number;
  private readonly pending = new Map<string, { path: Path; segs: Set<number> }>();
  private readonly pendingByParent = new Map<string, Set<string>>();
  private readonly wild = new Map<string, number[]>();
  private readonly indexWatch = new Map<string, { seg: number; index: number }[]>();
  private readonly sliceWatch = new Map<string, { seg: number; start: number; end: number; step: number }[]>();
  private readonly filterWatch = new Map<string, { seg: number; plan: FilterPlan }[]>();
  private readonly collectors = new Map<string, Collector>();
  private anchors: { seg: number; path: Path; canon: string }[] = [];
  private readonly childCount = new Map<string, number>();
  private readonly pendingResults = new Map<string, Path>();
  private readonly emitted = new Set<string>();
  private readonly deferred: DeferredItem[] = [];
  private readonly deferredSpillFiles: string[] = [];
  private readonly spillPreds: FilterPlan[] = [];
  private readonly tempFiles = new Set<string>();
  private readonly matches: Match[] = [];
  private events = 0;
  private spillSeq = 0;
  private didSpill = false;
  // 事件流为深度优先有序：规范路径随栈增量维护，单事件 O(1)
  private readonly canonStack: string[] = ['$'];

  constructor(prog: Program, opts: StreamOptions) {
    this.prog = prog;
    this.opts = opts;
    this.maxBuffered = opts.maxBuffered ?? DEFAULT_MAX_BUFFERED;
  }

  run(events: Iterable<Event>): StreamResult {
    this.addPending([], '$', 0, null);
    try {
      for (const ev of events) {
        if (this.opts.signal?.aborted) throw new CancelledError();
        this.events++;
        if (ev.type === 'enter') this.processEnter(ev.path, ev.kind);
        else this.processExit(ev.path, ev.value);
      }
      return { matches: this.matches, spilled: this.didSpill, eventsConsumed: this.events };
    } finally {
      this.cleanup();
    }
  }

  // ---- 事件处理 ----

  private processEnter(path: Path, kind: NodeKind): void {
    const depth = path.length;
    const canon = depth === 0 ? '$' : this.canonStack[depth - 1]! + encodeSeg(path[depth - 1]!);
    this.canonStack[depth] = canon;
    this.opts.budget?.consume('expand', 1, path);
    const toApply = new Set<number>();
    const pend = this.pending.get(canon);
    if (pend) {
      for (const s of pend.segs) toApply.add(s);
      this.pending.delete(canon);
    }
    if (depth > 0) {
      const parentCanon = this.canonStack[depth - 1]!;
      const idx = this.childCount.get(parentCanon) ?? 0;
      for (const s of this.wild.get(parentCanon) ?? []) toApply.add(s);
      for (const w of this.indexWatch.get(parentCanon) ?? []) if (w.index === idx) toApply.add(w.seg);
      for (const w of this.sliceWatch.get(parentCanon) ?? []) {
        if (idx >= w.start && idx < w.end && (idx - w.start) % w.step === 0) toApply.add(w.seg);
      }
      this.childCount.set(parentCanon, idx + 1);
    }
    for (const a of this.anchors) if (isPrefix(a.path, path)) toApply.add(a.seg + 1);
    for (const seg of toApply) this.applySegment(seg, path, kind, canon);
  }

  private processExit(path: Path, value: JsonValue): void {
    const canon = this.canonStack[path.length]!;
    const rp = this.pendingResults.get(canon);
    if (rp) {
      this.pendingResults.delete(canon);
      this.emit(rp, value);
    }
    if (path.length > 0) {
      const pcanon = this.canonStack[path.length - 1]!;
      const col = this.collectors.get(pcanon);
      if (col) this.collectorAdd(col, path, value);
      const fws = this.filterWatch.get(pcanon);
      if (fws) {
        for (const f of fws) {
          if (f.plan.pred(value, undefined, this.opts.budget)) this.continueWith(value, path, f.seg + 1);
        }
      }
    }
    const myCol = this.collectors.get(canon);
    if (myCol) {
      this.collectors.delete(canon);
      this.finalizeCollector(myCol);
    }
    if (path.length === 0) this.processDeferred(value);
    // 清理以该节点为作用域的全部状态
    this.wild.delete(canon);
    this.indexWatch.delete(canon);
    this.sliceWatch.delete(canon);
    this.filterWatch.delete(canon);
    this.childCount.delete(canon);
    const pset = this.pendingByParent.get(canon);
    if (pset) {
      for (const k of pset) this.pending.delete(k);
      this.pendingByParent.delete(canon);
    }
    this.anchors = this.anchors.filter((a) => a.canon !== canon);
  }

  private applySegment(segIdx: number, path: Path, kind: NodeKind, canon: string): void {
    const segs = this.prog.segments;
    if (segIdx === segs.length) {
      this.pendingResults.set(canon, path);
      return;
    }
    this.opts.budget?.consume('expand', 1, path);
    const s = segs[segIdx]!;
    switch (s.kind) {
      case 'child':
        if (kind === 'object') this.addPending([...path, s.key], canon + encodeSeg(s.key), segIdx + 1, canon);
        break;
      case 'wildcard':
        if (kind !== 'primitive') pushTo(this.wild, canon, segIdx + 1);
        break;
      case 'index':
        if (kind === 'array') {
          if (s.index >= 0) pushTo(this.indexWatch, canon, { seg: segIdx + 1, index: s.index });
          else this.ensureCollector(canon, path, segIdx, s);
        }
        break;
      case 'slice':
        if (kind === 'array') {
          if (needsContainerEnd(s)) this.ensureCollector(canon, path, segIdx, s);
          else {
            pushTo(this.sliceWatch, canon, {
              seg: segIdx + 1,
              start: s.start ?? 0,
              end: s.end ?? Number.POSITIVE_INFINITY,
              step: s.step,
            });
          }
        }
        break;
      case 'filter':
        if (kind !== 'primitive') {
          if (s.plan.usesRoot) this.ensureCollector(canon, path, segIdx, s);
          else pushTo(this.filterWatch, canon, { seg: segIdx, plan: s.plan });
        }
        break;
      case 'recursive':
        this.anchors.push({ seg: segIdx, path, canon });
        this.applySegment(segIdx + 1, path, kind, canon);
        break;
    }
  }

  // ---- 结果与续算 ----

  private emit(path: Path, value: JsonValue): void {
    const c = canonical(path);
    if (this.emitted.has(c)) return; // 递归下降等同路径命中去重
    this.emitted.add(c);
    const m: Match = { path, value };
    this.matches.push(m);
    this.opts.onMatch?.(m, this.events);
  }

  private continueWith(value: JsonValue, path: Path, seg: number): void {
    if (this.prog.hasRootRef) {
      this.deferredAdd({ path, value, seg });
    } else {
      evaluateFrom(value, path, this.prog, seg, { budget: this.opts.budget }, (m) => this.emit(m.path, m.value));
    }
  }

  // ---- 收集器（负下标/负切片/全局谓词的有界暂存） ----

  private ensureCollector(canon: string, path: Path, seg: number, segment: CompiledSegment): void {
    if (!this.collectors.has(canon)) {
      this.collectors.set(canon, { canon, path, seg, segment, items: [], spillFiles: [] });
    }
  }

  private collectorAdd(col: Collector, path: Path, value: JsonValue): void {
    col.items.push({ path, value });
    if (col.items.length > this.maxBuffered) {
      col.spillFiles.push(this.spill(col.items, col.path));
      col.items = [];
    }
  }

  private drainCollector(col: Collector): { path: Path; value: JsonValue }[] {
    const out: { path: Path; value: JsonValue }[] = [];
    for (const f of col.spillFiles) out.push(...this.readSpill<{ path: Path; value: JsonValue }>(f));
    out.push(...col.items);
    col.items = [];
    return out;
  }

  private finalizeCollector(col: Collector): void {
    const items = this.drainCollector(col);
    const s = col.segment;
    this.opts.budget?.consume('expand', items.length, col.path);
    if (s.kind === 'index') {
      const idx = items.length + s.index; // s.index < 0：尾部索引此刻才可判定
      if (idx >= 0 && idx < items.length) this.continueWith(items[idx]!.value, items[idx]!.path, col.seg + 1);
    } else if (s.kind === 'slice') {
      for (const i of sliceIndices(items.length, s.start, s.end, s.step)) {
        this.continueWith(items[i]!.value, items[i]!.path, col.seg + 1);
      }
    } else if (s.kind === 'filter') {
      // 引用 $ 的谓词：延期到根闭合后携带 root 求值
      for (const item of items) {
        this.deferredAdd({ path: item.path, value: item.value, seg: col.seg + 1, predFilter: s.plan });
      }
    }
  }

  // ---- 延期队列（需要全局信息的谓词） ----

  private deferredAdd(item: DeferredItem): void {
    this.deferred.push(item);
    if (this.deferred.length > this.maxBuffered) {
      const records = this.deferred.map((d) => ({
        path: d.path,
        value: d.value,
        seg: d.seg,
        pred: d.predFilter ? this.registerPred(d.predFilter) : undefined,
      }));
      this.deferredSpillFiles.push(this.spill(records, item.path));
      this.deferred.length = 0;
    }
  }

  private registerPred(plan: FilterPlan): number {
    this.spillPreds.push(plan);
    return this.spillPreds.length - 1;
  }

  private processDeferred(rootValue: JsonValue): void {
    const items: DeferredItem[] = [];
    for (const f of this.deferredSpillFiles) {
      for (const r of this.readSpill<{ path: Path; value: JsonValue; seg: number; pred?: number }>(f)) {
        items.push({ path: r.path, value: r.value, seg: r.seg, predFilter: r.pred === undefined ? undefined : this.spillPreds[r.pred] });
      }
    }
    items.push(...this.deferred);
    this.deferred.length = 0;
    for (const item of items) {
      if (this.opts.signal?.aborted) throw new CancelledError();
      if (item.predFilter && !item.predFilter.pred(item.value, rootValue, this.opts.budget)) continue;
      evaluateFrom(item.value, item.path, this.prog, item.seg, { budget: this.opts.budget, root: rootValue }, (m) =>
        this.emit(m.path, m.value),
      );
    }
  }

  // ---- 溢写与清理 ----

  private spill(records: object[], atPath: Path): string {
    if (!this.opts.spillDir) throw new BufferOverflowError(atPath, this.maxBuffered);
    mkdirSync(this.opts.spillDir, { recursive: true });
    const file = join(this.opts.spillDir, `spill-${process.pid}-${this.spillSeq++}.jsonl`);
    appendFileSync(file, records.map((r) => JSON.stringify(r)).join('\n') + '\n');
    this.tempFiles.add(file);
    this.didSpill = true;
    return file;
  }

  private readSpill<T>(file: string): T[] {
    const text = readFileSync(file, 'utf8');
    const lines = text.split('\n').filter((l) => l.length > 0);
    return lines.map((l) => JSON.parse(l) as T);
  }

  private cleanup(): void {
    for (const f of this.tempFiles) {
      try {
        if (existsSync(f)) unlinkSync(f);
      } catch {
        // 清理失败不遮蔽主错误
      }
    }
    this.tempFiles.clear();
    this.deferred.length = 0;
  }

  private addPending(path: Path, c: string, seg: number, parentCanon: string | null): void {
    let e = this.pending.get(c);
    if (!e) {
      e = { path, segs: new Set() };
      this.pending.set(c, e);
    }
    e.segs.add(seg);
    if (parentCanon !== null) {
      let set = this.pendingByParent.get(parentCanon);
      if (!set) {
        set = new Set();
        this.pendingByParent.set(parentCanon, set);
      }
      set.add(c);
    }
  }
}

/** 与 canonical() 的单段编码保持一致。 */
function encodeSeg(seg: string | number): string {
  return typeof seg === 'number' ? `[${seg}]` : `[${JSON.stringify(seg)}]`;
}

function pushTo<K, V>(map: Map<K, V[]>, key: K, value: V): void {
  let arr = map.get(key);
  if (!arr) {
    arr = [];
    map.set(key, arr);
  }
  arr.push(value);
}
