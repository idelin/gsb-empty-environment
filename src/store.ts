import { computeNumbering, subtreeEnd } from './numbering.ts';
import type {
  DanglingReferenceInfo,
  EditResult,
  Heading,
  LevelViolation,
  NumberChange,
  Reference,
  ResolvedReference,
  VersionedHeadingView,
} from './types.ts';

export interface InsertPosition {
  beforeId?: string;
  afterId?: string;
  index?: number;
}

export interface MoveTarget {
  beforeId?: string;
  afterId?: string;
}

export type Edit =
  | { kind: 'insert'; heading: Heading; position?: InsertPosition }
  | { kind: 'remove'; id: string; withSubtree?: boolean }
  | { kind: 'rename'; id: string; title: string }
  | { kind: 'changeLevel'; id: string; level: number }
  | { kind: 'move'; id: string; to: MoveTarget };

export interface ApplyOptions {
  onLevelJump?: 'report' | 'reject';
}

interface Snapshot {
  version: number;
  headings: Heading[];
  numbers: Map<string, string | null>;
  violations: LevelViolation[];
}

export class LevelJumpError extends Error {
  violations: LevelViolation[];

  constructor(violations: LevelViolation[]) {
    super(
      `level jump forbidden: ${violations
        .map((v) => `"${v.title}" (${v.headingId}) level ${v.actualLevel} > max ${v.maxAllowedLevel}`)
        .join('; ')}`,
    );
    this.name = 'LevelJumpError';
    this.violations = violations;
  }
}

function cloneHeadings(headings: Heading[]): Heading[] {
  return headings.map((h) => ({ ...h }));
}

export class OutlineStore {
  private headings: Heading[];
  private snapshots: Snapshot[] = [];
  private references: Map<string, Reference> = new Map();

  constructor(headings: Heading[] = []) {
    this.headings = cloneHeadings(headings);
    this.commit();
  }

  get version(): number {
    return this.snapshots.length;
  }

  private commit(): void {
    const { numbers, violations } = computeNumbering(this.headings);
    this.snapshots.push({
      version: this.snapshots.length + 1,
      headings: cloneHeadings(this.headings),
      numbers,
      violations,
    });
  }

  private current(): Snapshot {
    return this.snapshots[this.snapshots.length - 1];
  }

  getHeadings(): Heading[] {
    return cloneHeadings(this.current().headings);
  }

  getNumbering(): Map<string, string | null> {
    return new Map(this.current().numbers);
  }

  getNumber(id: string): string | null | undefined {
    const numbers = this.current().numbers;
    return numbers.has(id) ? numbers.get(id)! : undefined;
  }

  getViolations(): LevelViolation[] {
    return this.current().violations.map((v) => ({ ...v }));
  }

  findByTitle(title: string): Heading[] {
    return this.current().headings.filter((h) => h.title === title).map((h) => ({ ...h }));
  }

  applyEdit(edit: Edit, options: ApplyOptions = {}): EditResult {
    const before = this.current();
    const next = this.applyTo(cloneHeadings(this.headings), edit);
    const { violations } = computeNumbering(next);
    if (violations.length > 0 && (options.onLevelJump ?? 'report') === 'reject') {
      throw new LevelJumpError(violations);
    }
    this.headings = next;
    this.commit();
    const after = this.current();
    return {
      version: this.version,
      changes: diffNumbering(before, after),
      violations: after.violations.map((v) => ({ ...v })),
      danglingReferences: this.newlyDangling(before, after),
    };
  }

  private applyTo(arr: Heading[], edit: Edit): Heading[] {
    switch (edit.kind) {
      case 'insert': {
        if (arr.some((h) => h.id === edit.heading.id)) {
          throw new Error(`duplicate heading id: ${edit.heading.id}`);
        }
        const position = edit.position ?? {};
        let at = arr.length;
        if (position.beforeId !== undefined) {
          at = arr.findIndex((h) => h.id === position.beforeId);
          if (at < 0) throw new Error(`insert target not found: ${position.beforeId}`);
        } else if (position.afterId !== undefined) {
          const idx = arr.findIndex((h) => h.id === position.afterId);
          if (idx < 0) throw new Error(`insert target not found: ${position.afterId}`);
          at = idx + 1;
        } else if (position.index !== undefined) {
          at = position.index;
        }
        arr.splice(at, 0, { ...edit.heading });
        return arr;
      }
      case 'remove': {
        const idx = arr.findIndex((h) => h.id === edit.id);
        if (idx < 0) throw new Error(`heading not found: ${edit.id}`);
        const count = edit.withSubtree ? subtreeEnd(arr, idx) - idx : 1;
        arr.splice(idx, count);
        return arr;
      }
      case 'rename': {
        const heading = arr.find((h) => h.id === edit.id);
        if (!heading) throw new Error(`heading not found: ${edit.id}`);
        heading.title = edit.title;
        return arr;
      }
      case 'changeLevel': {
        if (!Number.isInteger(edit.level) || edit.level < 1) {
          throw new Error(`invalid level: ${edit.level}`);
        }
        const heading = arr.find((h) => h.id === edit.id);
        if (!heading) throw new Error(`heading not found: ${edit.id}`);
        heading.level = edit.level;
        return arr;
      }
      case 'move': {
        const start = arr.findIndex((h) => h.id === edit.id);
        if (start < 0) throw new Error(`heading not found: ${edit.id}`);
        const end = subtreeEnd(arr, start);
        const slice = arr.slice(start, end);
        const rest = [...arr.slice(0, start), ...arr.slice(end)];
        let at: number;
        if (edit.to.beforeId !== undefined) {
          at = rest.findIndex((h) => h.id === edit.to.beforeId);
          if (at < 0) {
            throw new Error(`move target not found or inside moved subtree: ${edit.to.beforeId}`);
          }
        } else if (edit.to.afterId !== undefined) {
          const idx = rest.findIndex((h) => h.id === edit.to.afterId);
          if (idx < 0) {
            throw new Error(`move target not found or inside moved subtree: ${edit.to.afterId}`);
          }
          at = idx + 1;
        } else {
          at = rest.length;
        }
        rest.splice(at, 0, ...slice);
        return rest;
      }
    }
  }

  addReference(ref: Reference): void {
    if (this.references.has(ref.id)) {
      throw new Error(`duplicate reference id: ${ref.id}`);
    }
    if (ref.pinnedVersion !== undefined) {
      if (!Number.isInteger(ref.pinnedVersion) || ref.pinnedVersion < 1 || ref.pinnedVersion > this.version) {
        throw new Error(`pinnedVersion out of range: ${ref.pinnedVersion}`);
      }
    }
    this.references.set(ref.id, { ...ref });
  }

  removeReference(refId: string): void {
    this.references.delete(refId);
  }

  resolveReference(refId: string): ResolvedReference {
    const ref = this.references.get(refId);
    if (!ref) throw new Error(`unknown reference: ${refId}`);
    const current = this.current();
    const heading = current.headings.find((h) => h.id === ref.targetId);
    const pinned =
      ref.pinnedVersion !== undefined ? this.lookupAt(ref.pinnedVersion, ref.targetId) : undefined;
    if (!heading) {
      return {
        status: 'dangling',
        referenceId: ref.id,
        targetId: ref.targetId,
        lastKnown: pinned ?? this.lastKnown(ref.targetId),
      };
    }
    const number = current.numbers.get(heading.id) ?? null;
    return {
      status: 'ok',
      referenceId: ref.id,
      targetId: ref.targetId,
      number,
      title: heading.title,
      pinned: pinned ?? undefined,
      changedSincePinned:
        pinned != null && (pinned.number !== number || pinned.title !== heading.title),
    };
  }

  listDanglingReferences(): DanglingReferenceInfo[] {
    const out: DanglingReferenceInfo[] = [];
    for (const ref of this.references.values()) {
      const resolved = this.resolveReference(ref.id);
      if (resolved.status === 'dangling') {
        out.push({ referenceId: ref.id, targetId: ref.targetId, lastKnown: resolved.lastKnown });
      }
    }
    return out;
  }

  private lookupAt(version: number, targetId: string): VersionedHeadingView | null {
    const snapshot = this.snapshots[version - 1];
    if (!snapshot) return null;
    const heading = snapshot.headings.find((h) => h.id === targetId);
    if (!heading) return null;
    return {
      version,
      number: snapshot.numbers.get(targetId) ?? null,
      title: heading.title,
    };
  }

  private lastKnown(targetId: string): VersionedHeadingView | null {
    for (let i = this.snapshots.length - 1; i >= 0; i--) {
      const view = this.lookupAt(this.snapshots[i].version, targetId);
      if (view) return view;
    }
    return null;
  }

  private newlyDangling(before: Snapshot, after: Snapshot): DanglingReferenceInfo[] {
    const out: DanglingReferenceInfo[] = [];
    for (const ref of this.references.values()) {
      const had = before.headings.some((h) => h.id === ref.targetId);
      const has = after.headings.some((h) => h.id === ref.targetId);
      if (had && !has) {
        out.push({
          referenceId: ref.id,
          targetId: ref.targetId,
          lastKnown: this.lastKnown(ref.targetId),
        });
      }
    }
    return out;
  }
}

function diffNumbering(before: Snapshot, after: Snapshot): NumberChange[] {
  const changes: NumberChange[] = [];
  const titleOf = (snapshot: Snapshot, id: string): string =>
    snapshot.headings.find((h) => h.id === id)?.title ?? '';
  for (const [id, newNumber] of after.numbers) {
    if (!before.numbers.has(id)) {
      changes.push({ id, title: titleOf(after, id), oldNumber: null, newNumber, kind: 'added' });
    } else if (before.numbers.get(id) !== newNumber) {
      changes.push({
        id,
        title: titleOf(after, id),
        oldNumber: before.numbers.get(id)!,
        newNumber,
        kind: 'changed',
      });
    }
  }
  for (const [id, oldNumber] of before.numbers) {
    if (!after.numbers.has(id)) {
      changes.push({ id, title: titleOf(before, id), oldNumber, newNumber: null, kind: 'removed' });
    }
  }
  return changes;
}
