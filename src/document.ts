import {
  AmbiguousTitleError,
  CyclicMoveError,
  DuplicateHeadingIdError,
  HeadingNotFoundError,
  InvalidLevelError,
  LevelSkipError,
} from './errors';
import {
  EditResult,
  Heading,
  HeadingInput,
  LevelSkip,
  LevelSkipFix,
  NumberChange,
  OutlineItem,
  Reference,
  ResolvedReference,
} from './types';

interface Mutation {
  affectedFrom: number;
  removed?: Heading[];
  added?: Heading[];
}

/** 找到第一处跳级的下标；没有则返回 -1。 */
function firstSkipIndex(levels: number[]): number {
  let prev = 0;
  for (let i = 0; i < levels.length; i++) {
    if (levels[i] > prev + 1) return i;
    prev = levels[i];
  }
  return -1;
}

function findLevelSkips(headings: Heading[]): LevelSkip[] {
  const skips: LevelSkip[] = [];
  let prev = 0;
  for (const h of headings) {
    if (h.level > prev + 1) {
      skips.push({ headingId: h.id, maxAllowedLevel: prev + 1, actualLevel: h.level });
    }
    prev = h.level;
  }
  return skips;
}

/**
 * 为第一处跳级计算最小修复范围：
 * 候选方案：A1 仅将违规标题的子树降级；A2 将跳级块整体降级（恒有效）；
 * B 将前文相关小节整体升级（可能级联，未必收敛）。
 * 取涉及标题更少者，平局优先更局部的方案。只给建议，绝不自动应用。
 */
function computeMinimalFix(headings: Heading[], skipAt: number): LevelSkipFix {
  const levels = headings.map((h) => h.level);
  const prev = skipAt === 0 ? 0 : levels[skipAt - 1];
  const skipBy = levels[skipAt] - (prev + 1);

  const candidates: LevelSkipFix[] = [];

  // A1：只下调违规标题自身的子树
  const subtree: number[] = [skipAt];
  for (let i = skipAt + 1; i < levels.length && levels[i] > levels[skipAt]; i++) subtree.push(i);
  const simA1 = levels.slice();
  for (const i of subtree) simA1[i] -= skipBy;
  if (firstSkipIndex(simA1) === -1) {
    candidates.push({
      direction: 'lower-following',
      adjustments: subtree.map((i) => ({ id: headings[i].id, delta: -skipBy })),
    });
  }

  // A2：下调整个跳级块（层级高于 prev 的连续段），恒有效
  let end = skipAt;
  while (end < levels.length && levels[end] > prev) end++;
  const optionA2 = [];
  for (let i = skipAt; i < end; i++) optionA2.push({ id: headings[i].id, delta: -skipBy });
  candidates.push({ direction: 'lower-following', adjustments: optionA2 });

  // B：升级前文相关小节，迭代直到不再跳级
  const sim = levels.slice();
  const deltas = new Map<number, number>();
  let optionBValid = false;
  for (let guard = 0; guard <= levels.length; guard++) {
    const s = firstSkipIndex(sim);
    if (s === -1) {
      optionBValid = true;
      break;
    }
    if (s === 0) break; // 文首跳级无法通过升级前文修复
    const p = sim[s - 1];
    const by = sim[s] - (p + 1);
    let start = s - 1;
    while (start - 1 >= 0 && sim[start - 1] >= p) start--;
    for (let k = start; k < s; k++) {
      sim[k] += by;
      deltas.set(k, (deltas.get(k) ?? 0) + by);
    }
  }
  const optionB = optionBValid
    ? [...deltas.entries()]
        .sort((a, b) => a[0] - b[0])
        .map(([i, delta]) => ({ id: headings[i].id, delta }))
    : null;
  if (optionB) candidates.push({ direction: 'raise-preceding', adjustments: optionB });

  candidates.sort((a, b) => a.adjustments.length - b.adjustments.length);
  return candidates[0];
}

export class NumberedDocument {
  private headings: Heading[] = [];
  private byId = new Map<string, Heading>();
  private numbers = new Map<string, string>();
  private references = new Map<string, Reference>();
  private idSeq = 0;
  private refSeq = 0;
  /** 每次成功编辑递增；引用快照记录创建时的版本。 */
  version = 0;

  constructor(initial: HeadingInput[] = []) {
    for (const input of initial) this.headings.push(this.makeHeading(input));
    const skips = findLevelSkips(this.headings);
    if (skips.length > 0) {
      throw new LevelSkipError(skips, computeMinimalFix(this.headings, this.skipIndexOf(skips[0])));
    }
    for (const h of this.headings) this.byId.set(h.id, h);
    this.renumberFrom(0);
  }

  // ---------- 查询 ----------

  getOutline(): OutlineItem[] {
    return this.headings.map((h) => ({ ...h, number: this.numbers.get(h.id)! }));
  }

  getNumber(id: string): string {
    return this.numbers.get(this.require(id).id)!;
  }

  getHeading(id: string): Heading {
    return { ...this.require(id) };
  }

  /** 同名标题全部命中，交给调用方消歧。 */
  findByTitle(title: string): Heading[] {
    return this.headings.filter((h) => h.title === title).map((h) => ({ ...h }));
  }

  getByTitle(title: string): Heading {
    const matches = this.findByTitle(title);
    if (matches.length === 0) throw new HeadingNotFoundError(`title=${title}`);
    if (matches.length > 1) {
      throw new AmbiguousTitleError(title, matches.map((h) => h.id));
    }
    return matches[0];
  }

  // ---------- 编辑 ----------

  rename(id: string, title: string): EditResult {
    this.require(id).title = title;
    this.version++;
    return { ok: true, version: this.version, changes: [], danglingReferences: this.danglingReferences() };
  }

  append(input: HeadingInput): EditResult {
    return this.insert(this.headings.length, input);
  }

  insert(index: number, input: HeadingInput): EditResult {
    const heading = this.makeHeading(input);
    if (this.byId.has(heading.id)) throw new DuplicateHeadingIdError(heading.id);
    return this.commitEdit(() => {
      this.headings.splice(index, 0, heading);
      return { affectedFrom: index, added: [heading] };
    });
  }

  /** 调整标题（连同其子树）的绝对层级；产生跳级时整体回滚并返回最小修复范围。 */
  setLevel(id: string, newLevel: number): EditResult {
    const root = this.require(id);
    if (!Number.isInteger(newLevel) || newLevel < 1) {
      return { ok: false, error: new InvalidLevelError(newLevel) };
    }
    const delta = newLevel - root.level;
    if (delta === 0) {
      return { ok: true, version: this.version, changes: [], danglingReferences: this.danglingReferences() };
    }
    return this.commitEdit(() => {
      const start = this.headings.indexOf(root);
      const end = this.sectionEnd(start);
      for (let i = start; i < end; i++) this.headings[i].level += delta;
      return { affectedFrom: start };
    });
  }

  /**
   * 把 id 的子树移动到 newParentId 之下（null 表示顶层）的第 childIndex 个孩子位置
   * （缺省追加到末尾）。层级随新父级自动平移，编号随树重排。
   */
  move(id: string, newParentId: string | null, childIndex?: number): EditResult {
    const root = this.require(id);
    if (newParentId !== null) {
      const parent = this.require(newParentId);
      const start = this.headings.indexOf(root);
      const end = this.sectionEnd(start);
      const parentIdx = this.headings.indexOf(parent);
      if (parentIdx >= start && parentIdx < end) {
        return { ok: false, error: new CyclicMoveError(id, newParentId) };
      }
    }
    return this.commitEdit(() => {
      const start = this.headings.indexOf(root);
      const end = this.sectionEnd(start);
      const block = this.headings.splice(start, end - start);
      let insertAt: number;
      let targetLevel: number;
      if (newParentId === null) {
        targetLevel = 1;
        insertAt = this.rootInsertPosition(childIndex);
      } else {
        const parent = this.byId.get(newParentId)!;
        targetLevel = parent.level + 1;
        insertAt = this.childInsertPosition(parent, childIndex);
      }
      const delta = targetLevel - root.level;
      for (const h of block) h.level += delta;
      this.headings.splice(insertAt, 0, ...block);
      return { affectedFrom: Math.min(start, insertAt) };
    });
  }

  /**
   * 删除标题。默认只删自身、子标题整体上移一级（保持树合法的最小结构修复）；
   * cascade 为 true 时整棵子树删除。指向被删标题的引用变为悬空。
   */
  remove(id: string, opts: { cascade?: boolean } = {}): EditResult {
    const root = this.require(id);
    return this.commitEdit(() => {
      const start = this.headings.indexOf(root);
      const end = this.sectionEnd(start);
      if (opts.cascade) {
        const removed = this.headings.splice(start, end - start);
        return { affectedFrom: start, removed };
      }
      const [removed] = this.headings.splice(start, 1);
      for (let i = start; i < end - 1; i++) this.headings[i].level -= 1;
      return { affectedFrom: start, removed: [removed] };
    });
  }

  // ---------- 引用 ----------

  /** 建立外部引用：绑定稳定 ID，并记录当前版本下的编号/标题快照。 */
  addReference(targetId: string, refId?: string): Reference {
    const target = this.require(targetId);
    const ref: Reference = {
      id: refId ?? `r${++this.refSeq}`,
      targetId,
      createdVersion: this.version,
      snapshotNumber: this.numbers.get(targetId)!,
      snapshotTitle: target.title,
    };
    if (this.references.has(ref.id)) throw new DuplicateHeadingIdError(`ref:${ref.id}`);
    this.references.set(ref.id, ref);
    return { ...ref };
  }

  resolveReference(refId: string): ResolvedReference {
    const ref = this.references.get(refId);
    if (!ref) throw new HeadingNotFoundError(`ref:${refId}`);
    return this.resolve(ref);
  }

  resolveReferences(): ResolvedReference[] {
    return [...this.references.values()].map((r) => this.resolve(r));
  }

  danglingReferences(): ResolvedReference[] {
    return this.resolveReferences().filter((r) => r.status === 'dangling');
  }

  // ---------- 内部 ----------

  private resolve(ref: Reference): ResolvedReference {
    const snapshotDisplay = `${ref.snapshotNumber} ${ref.snapshotTitle}`;
    const target = this.byId.get(ref.targetId);
    if (!target) {
      return {
        reference: { ...ref },
        status: 'dangling',
        current: null,
        stale: true,
        display: `${snapshotDisplay} (已失效)`,
        snapshotDisplay,
      };
    }
    const current = { number: this.numbers.get(target.id)!, title: target.title };
    const stale = current.number !== ref.snapshotNumber || current.title !== ref.snapshotTitle;
    return {
      reference: { ...ref },
      status: 'ok',
      current,
      stale,
      display: `${current.number} ${current.title}`,
      snapshotDisplay,
    };
  }

  private makeHeading(input: HeadingInput): Heading {
    if (!Number.isInteger(input.level) || input.level < 1) {
      throw new InvalidLevelError(input.level);
    }
    return { id: input.id ?? `h${++this.idSeq}`, title: input.title, level: input.level };
  }

  private require(id: string): Heading {
    const h = this.byId.get(id);
    if (!h) throw new HeadingNotFoundError(id);
    return h;
  }

  private skipIndexOf(skip: LevelSkip): number {
    return this.headings.findIndex((h) => h.id === skip.headingId);
  }

  /** [index, ...) 中标题所属小节的结束下标（不含）。 */
  private sectionEnd(index: number): number {
    const level = this.headings[index].level;
    let end = index + 1;
    while (end < this.headings.length && this.headings[end].level > level) end++;
    return end;
  }

  private rootInsertPosition(childIndex?: number): number {
    if (childIndex === undefined) return this.headings.length;
    let seen = 0;
    for (let i = 0; i < this.headings.length; i++) {
      if (this.headings[i].level === 1) {
        if (seen === childIndex) return i;
        seen++;
      }
    }
    return this.headings.length;
  }

  private childInsertPosition(parent: Heading, childIndex?: number): number {
    const parentIdx = this.headings.indexOf(parent);
    const end = this.sectionEnd(parentIdx);
    if (childIndex === undefined) return end;
    let seen = 0;
    for (let i = parentIdx + 1; i < end; i++) {
      if (this.headings[i].level === parent.level + 1) {
        if (seen === childIndex) return i;
        seen++;
      }
    }
    return end;
  }

  /**
   * 增量编号：从受影响位置起重建计数器并向后扫描，
   * 只收集与已存编号不一致的项作为变化输出。
   */
  private renumberFrom(startIndex: number): NumberChange[] {
    const counters: number[] = [];
    for (let i = 0; i < startIndex; i++) this.bump(counters, this.headings[i].level);
    const changes: NumberChange[] = [];
    for (let i = startIndex; i < this.headings.length; i++) {
      const h = this.headings[i];
      this.bump(counters, h.level);
      const num = counters.slice(0, h.level).join('.');
      const old = this.numbers.get(h.id) ?? null;
      if (old !== num) {
        changes.push({ id: h.id, title: h.title, oldNumber: old, newNumber: num });
        this.numbers.set(h.id, num);
      }
    }
    return changes;
  }

  private bump(counters: number[], level: number): void {
    counters[level - 1] = (counters[level - 1] ?? 0) + 1;
    counters.length = level;
  }

  /**
   * 事务化执行一次结构编辑：先快照，变更后校验跳级；
   * 违例则整体回滚并返回最小修复范围，文档保持原状。
   */
  private commitEdit(mutate: () => Mutation): EditResult {
    const savedOrder = this.headings.slice();
    const savedLevels = new Map(this.headings.map((h) => [h.id, h.level] as const));

    const mutation = mutate();

    const skips = findLevelSkips(this.headings);
    if (skips.length > 0) {
      const fix = computeMinimalFix(this.headings, this.skipIndexOf(skips[0]));
      this.headings = savedOrder;
      for (const h of this.headings) h.level = savedLevels.get(h.id)!;
      return { ok: false, error: new LevelSkipError(skips, fix) };
    }

    const changes: NumberChange[] = [];
    for (const removed of mutation.removed ?? []) {
      this.byId.delete(removed.id);
      const oldNumber = this.numbers.get(removed.id) ?? null;
      this.numbers.delete(removed.id);
      changes.push({ id: removed.id, title: removed.title, oldNumber, newNumber: null });
    }
    for (const added of mutation.added ?? []) this.byId.set(added.id, added);

    changes.push(...this.renumberFrom(mutation.affectedFrom));
    this.version++;
    return { ok: true, version: this.version, changes, danglingReferences: this.danglingReferences() };
  }
}
