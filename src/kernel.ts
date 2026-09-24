/**
 * 模板编辑内核：嵌套占位 + 镜像引用 + 纯函数变换。
 *
 * - 公开偏移一律为 UTF-16 码元下标（即 JS 字符串下标），范围禁止切开代理对。
 * - 镜像占位引用一个或多个源占位，可挂纯函数变换；源变化时按有向依赖拓扑序增量更新。
 * - 循环引用在编译期（compile / 首次 edit）拒绝，并报出具体环链。
 * - edit 是原子的：文本、占位树、依赖、撤销状态一次提交；变换中途抛错全部回滚。
 * - 变换执行期间禁止重入编辑（ReentrancyError），重入导致的失败同样整体回滚。
 */

export type PlaceholderId = string;
export type TransformFn = (...texts: string[]) => string;

export class CycleError extends Error {
  readonly chain: PlaceholderId[];
  constructor(chain: PlaceholderId[]) {
    super(`Circular placeholder reference detected: ${chain.join(' -> ')}`);
    this.name = 'CycleError';
    this.chain = chain;
  }
}

export class ReentrancyError extends Error {
  constructor() {
    super('Re-entrant edit while a transform is running is forbidden');
    this.name = 'ReentrancyError';
  }
}

export class UnknownPlaceholderError extends Error {
  constructor(id: PlaceholderId) {
    super(`Unknown placeholder: ${id}`);
    this.name = 'UnknownPlaceholderError';
  }
}

export class InvalidatedPlaceholderError extends Error {
  constructor(id: PlaceholderId) {
    super(`Placeholder was invalidated by an ancestor edit: ${id}`);
    this.name = 'InvalidatedPlaceholderError';
  }
}

export class InvalidRangeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'InvalidRangeError';
  }
}

export class FinalizedError extends Error {
  constructor() {
    super('Kernel has exited placeholder mode; only plain text remains');
    this.name = 'FinalizedError';
  }
}

interface Placeholder {
  id: PlaceholderId;
  start: number;
  end: number;
  parent: PlaceholderId | null;
  children: PlaceholderId[];
  sources: PlaceholderId[];
  transform: TransformFn | null;
  transformName: string | null;
  valid: boolean;
}

interface Snapshot {
  text: string;
  placeholders: Map<PlaceholderId, Placeholder>;
}

const isHighSurrogate = (code: number): boolean => code >= 0xd800 && code <= 0xdbff;
const isLowSurrogate = (code: number): boolean => code >= 0xdc00 && code <= 0xdfff;

function clonePlaceholders(src: Map<PlaceholderId, Placeholder>): Map<PlaceholderId, Placeholder> {
  const out = new Map<PlaceholderId, Placeholder>();
  for (const [id, p] of src) {
    out.set(id, { ...p, children: [...p.children], sources: [...p.sources] });
  }
  return out;
}

export class TemplateKernel {
  #text: string;
  #placeholders = new Map<PlaceholderId, Placeholder>();
  #dependents = new Map<PlaceholderId, PlaceholderId[]>();
  #topo: PlaceholderId[] = [];
  #dirty = true;
  #undo: Snapshot[] = [];
  #busy = false;
  #finished = false;

  constructor(text: string) {
    this.#text = text;
  }

  get text(): string {
    this.#assertActive();
    return this.#text;
  }

  get undoDepth(): number {
    return this.#undo.length;
  }

  /** 注册主占位（可嵌套；parentId 省略时自动取最内层包含者）。 */
  addPlaceholder(id: PlaceholderId, start: number, end: number, parentId: PlaceholderId | null = null): this {
    return this.#add(id, start, end, parentId, [], null, null, false);
  }

  /**
   * 注册镜像占位：内容 = transform(...源占位文本)，无 transform 时为纯镜像。
   * 注册即同步一次初始内容（同步是原子的：变换抛错则注册不产生任何副作用）。
   */
  addMirror(
    id: PlaceholderId,
    start: number,
    end: number,
    sources: PlaceholderId | PlaceholderId[],
    transform: TransformFn | null = null,
    transformName: string | null = null,
  ): this {
    const sourceList = Array.isArray(sources) ? sources : [sources];
    return this.#add(id, start, end, null, sourceList, transform, transformName, true);
  }

  /** 编译有向依赖：构建拓扑序并拒绝循环引用。edit 前会自动调用，也可显式调用做静态检查。 */
  compile(): void {
    this.#assertActive();
    this.#compile();
  }

  /** 原子编辑：替换占位文本并沿依赖增量传播；任何变换抛错都会整体回滚。 */
  edit(id: PlaceholderId, newText: string): void {
    this.#assertActive();
    if (this.#busy) throw new ReentrancyError();
    this.#busy = true;
    const snapshot = this.#snapshot();
    try {
      this.#compile();
      const target = this.#getValid(id);
      this.#invalidateDescendants(target);
      this.#cascadeBrokenMirrors();
      this.#replaceRange(target.start, target.end, newText);
      this.#propagate(id);
      this.#undo.push(snapshot);
    } catch (err) {
      this.#restore(snapshot);
      throw err;
    } finally {
      this.#busy = false;
    }
  }

  /** 撤销上一次成功提交的编辑，恢复文本、占位树（含被覆盖子占位的原身份）与依赖。 */
  undo(): boolean {
    this.#assertActive();
    if (this.#busy) throw new ReentrancyError();
    const snapshot = this.#undo.pop();
    if (!snapshot) return false;
    this.#restore(snapshot);
    return true;
  }

  /** 退出占位模式：丢弃全部占位/依赖/撤销状态，保留最终纯文本。 */
  finalize(): string {
    this.#assertActive();
    this.#finished = true;
    const out = this.#text;
    this.#placeholders.clear();
    this.#dependents.clear();
    this.#topo = [];
    this.#undo = [];
    return out;
  }

  placeholderText(id: PlaceholderId): string {
    const p = this.#getValid(id);
    return this.#text.slice(p.start, p.end);
  }

  placeholderRange(id: PlaceholderId): { start: number; end: number } {
    const p = this.#getValid(id);
    return { start: p.start, end: p.end };
  }

  isValid(id: PlaceholderId): boolean {
    const p = this.#placeholders.get(id);
    return p !== undefined && p.valid;
  }

  // ---- 内部实现 ----

  #assertActive(): void {
    if (this.#finished) throw new FinalizedError();
  }

  #getValid(id: PlaceholderId): Placeholder {
    const p = this.#placeholders.get(id);
    if (!p) throw new UnknownPlaceholderError(id);
    if (!p.valid) throw new InvalidatedPlaceholderError(id);
    return p;
  }

  #assertRange(start: number, end: number): void {
    if (!Number.isInteger(start) || !Number.isInteger(end) || start < 0 || end < start || end > this.#text.length) {
      throw new InvalidRangeError(`Invalid range [${start}, ${end}) for text of length ${this.#text.length}`);
    }
    if (
      start > 0 &&
      isHighSurrogate(this.#text.charCodeAt(start - 1)) &&
      isLowSurrogate(this.#text.charCodeAt(start))
    ) {
      throw new InvalidRangeError(`Range start ${start} splits a surrogate pair`);
    }
    if (
      end > 0 &&
      end < this.#text.length &&
      isHighSurrogate(this.#text.charCodeAt(end - 1)) &&
      isLowSurrogate(this.#text.charCodeAt(end))
    ) {
      throw new InvalidRangeError(`Range end ${end} splits a surrogate pair`);
    }
  }

  #add(
    id: PlaceholderId,
    start: number,
    end: number,
    parentId: PlaceholderId | null,
    sources: PlaceholderId[],
    transform: TransformFn | null,
    transformName: string | null,
    syncInitial: boolean,
  ): this {
    this.#assertActive();
    if (this.#busy) throw new ReentrancyError();
    if (this.#placeholders.has(id)) throw new Error(`Duplicate placeholder id: ${id}`);
    this.#assertRange(start, end);

    const parent = parentId !== null ? this.#getValid(parentId) : this.#innermostContainer(start, end);
    if (parent && (start < parent.start || end > parent.end)) {
      throw new InvalidRangeError(`Placeholder ${id} escapes parent ${parent.id}`);
    }
    for (const q of this.#placeholders.values()) {
      if (!q.valid || q === parent) continue;
      const disjoint = end <= q.start || start >= q.end;
      const contained = q.start <= start && q.end >= end;
      const contains = start <= q.start && end >= q.end;
      if (!disjoint && !contained && !contains) {
        throw new InvalidRangeError(`Placeholder ${id} partially overlaps ${q.id}`);
      }
    }

    // 先算初始内容再落地：变换抛错时不产生任何副作用。
    // 源允许前向引用（注册时尚不存在）；缺失源的镜像推迟同步，由 compile 拒绝。
    let initial: string | null = null;
    const resolved = sources.map((s) => this.#placeholders.get(s));
    if (syncInitial && resolved.every((sp) => sp !== undefined && sp.valid)) {
      const texts = resolved.map((sp) => this.#text.slice((sp as Placeholder).start, (sp as Placeholder).end));
      this.#busy = true;
      try {
        initial = transform ? transform(...texts) : texts[0];
      } finally {
        this.#busy = false;
      }
    }

    const ph: Placeholder = {
      id,
      start,
      end,
      parent: parent ? parent.id : null,
      children: [],
      sources,
      transform,
      transformName: transformName ?? (transform && transform.name ? transform.name : null),
      valid: true,
    };
    this.#placeholders.set(id, ph);
    if (parent) parent.children.push(id);
    for (const q of this.#placeholders.values()) {
      if (q !== ph && q.valid && q.parent === null && ph.start <= q.start && q.end <= ph.end && q.id !== parent?.id) {
        q.parent = id;
        ph.children.push(q.id);
      }
    }
    if (initial !== null) this.#replaceRange(start, end, initial);
    this.#dirty = true;
    return this;
  }

  #innermostContainer(start: number, end: number): Placeholder | null {
    let best: Placeholder | null = null;
    for (const q of this.#placeholders.values()) {
      if (!q.valid) continue;
      if (q.start <= start && end <= q.end && (best === null || q.end - q.start < best.end - best.start)) {
        best = q;
      }
    }
    return best;
  }

  #invalidateDescendants(p: Placeholder): void {
    for (const childId of p.children) {
      const child = this.#placeholders.get(childId);
      if (!child || !child.valid) continue;
      child.valid = false;
      this.#invalidateDescendants(child);
    }
  }

  /** 源被覆盖失效的镜像级联失效（不动点迭代，处理镜像链）。 */
  #cascadeBrokenMirrors(): void {
    let changed = true;
    while (changed) {
      changed = false;
      for (const p of this.#placeholders.values()) {
        if (!p.valid || p.sources.length === 0) continue;
        for (const s of p.sources) {
          const sp = this.#placeholders.get(s);
          if (!sp || !sp.valid) {
            p.valid = false;
            this.#dirty = true;
            changed = true;
            break;
          }
        }
      }
    }
  }

  /** 用 replacement 替换 [start, end)，并平移/伸缩所有占位区间。范围调用前已保证不切开代理对。 */
  #replaceRange(start: number, end: number, replacement: string): void {
    const delta = replacement.length - (end - start);
    if (delta === 0 && this.#text.slice(start, end) === replacement) return;
    this.#text = this.#text.slice(0, start) + replacement + this.#text.slice(end);
    if (delta !== 0) {
      for (const q of this.#placeholders.values()) {
        if (q.end <= start) continue;
        if (q.start >= end) {
          q.start += delta;
          q.end += delta;
        } else {
          q.end += delta;
        }
      }
    }
  }

  #compile(): void {
    if (!this.#dirty) return;
    for (const p of this.#placeholders.values()) {
      if (!p.valid) continue;
      for (const s of p.sources) {
        if (!this.#placeholders.has(s)) throw new UnknownPlaceholderError(s);
      }
    }
    const dependents = new Map<PlaceholderId, PlaceholderId[]>();
    for (const p of this.#placeholders.values()) {
      if (!p.valid) continue;
      for (const s of p.sources) {
        let list = dependents.get(s);
        if (!list) {
          list = [];
          dependents.set(s, list);
        }
        list.push(p.id);
      }
    }
    const topo: PlaceholderId[] = [];
    const state = new Map<PlaceholderId, 1 | 2>(); // 1=visiting, 2=done
    const stack: PlaceholderId[] = [];
    const visit = (id: PlaceholderId): void => {
      const st = state.get(id);
      if (st === 2) return;
      if (st === 1) {
        const idx = stack.indexOf(id);
        throw new CycleError([...stack.slice(idx), id]);
      }
      state.set(id, 1);
      stack.push(id);
      const p = this.#placeholders.get(id);
      if (p && p.valid) {
        for (const s of p.sources) {
          const sp = this.#placeholders.get(s);
          if (sp && sp.valid) visit(s);
        }
      }
      stack.pop();
      state.set(id, 2);
      topo.push(id);
    };
    for (const p of this.#placeholders.values()) {
      if (p.valid && p.sources.length > 0) visit(p.id);
    }
    this.#dependents = dependents;
    this.#topo = topo;
    this.#dirty = false;
  }

  /** 从 changedId 出发沿依赖边收集下游，按拓扑序逐个重算镜像（菱形共享节点只算一次）。 */
  #propagate(changedId: PlaceholderId): void {
    const downstream = new Set<PlaceholderId>();
    const queue: PlaceholderId[] = [changedId];
    while (queue.length > 0) {
      const cur = queue.pop() as PlaceholderId;
      for (const dep of this.#dependents.get(cur) ?? []) {
        if (!downstream.has(dep)) {
          downstream.add(dep);
          queue.push(dep);
        }
      }
    }
    if (downstream.size === 0) return;
    for (const id of this.#topo) {
      if (!downstream.has(id)) continue;
      const m = this.#placeholders.get(id);
      if (!m || !m.valid) continue;
      const texts: string[] = [];
      let broken = false;
      for (const s of m.sources) {
        const sp = this.#placeholders.get(s);
        if (!sp || !sp.valid) {
          broken = true;
          break;
        }
        texts.push(this.#text.slice(sp.start, sp.end));
      }
      if (broken) {
        m.valid = false; // 源被覆盖失效，镜像级联失效
        this.#dirty = true;
        continue;
      }
      let next: string;
      try {
        next = m.transform ? m.transform(...texts) : texts[0];
      } catch (err) {
        if (err instanceof ReentrancyError) throw err;
        const name = m.transformName ?? 'anonymous';
        throw new Error(`Transform "${name}" for placeholder "${id}" failed: ${(err as Error).message}`, {
          cause: err,
        });
      }
      if (next !== this.#text.slice(m.start, m.end)) {
        this.#invalidateDescendants(m);
        this.#replaceRange(m.start, m.end, next);
      }
    }
  }

  #snapshot(): Snapshot {
    return { text: this.#text, placeholders: clonePlaceholders(this.#placeholders) };
  }

  #restore(snapshot: Snapshot): void {
    this.#text = snapshot.text;
    this.#placeholders = clonePlaceholders(snapshot.placeholders);
    this.#dirty = true;
  }
}
