export interface SelectionRange {
  anchor: number;
  active: number;
}

export interface Change {
  start: number;
  end: number;
  text: string;
}

export interface EditorSnapshot {
  text: string;
  selection: SelectionRange;
}

export interface BranchSummary {
  id: string;
  text: string;
  selection: SelectionRange;
}

export interface ReplayState extends EditorSnapshot {
  nodeId: string;
  parentId: string | null;
}

interface HistoryNode {
  id: string;
  parentId: string | null;
  childIds: string[];
  forward: Change[];
  selection: SelectionRange;
  baselineText?: string;
}

function assertNonNegativeInteger(value: number, name: string): void {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new RangeError(`${name} must be a non-negative safe integer`);
  }
}

function assertString(value: string, name: string): void {
  if (typeof value !== 'string') {
    throw new TypeError(`${name} must be a string`);
  }
}

function isHighSurrogate(code: number | undefined): boolean {
  return code !== undefined && code >= 0xd800 && code <= 0xdbff;
}

function isLowSurrogate(code: number | undefined): boolean {
  return code !== undefined && code >= 0xdc00 && code <= 0xdfff;
}

function assertUtf16Boundary(text: string, offset: number, name: string): void {
  assertNonNegativeInteger(offset, name);
  if (offset > text.length) {
    throw new RangeError(`${name} is outside the text`);
  }
  if (isHighSurrogate(text.charCodeAt(offset - 1)) && isLowSurrogate(text.charCodeAt(offset))) {
    throw new RangeError(`${name} splits a UTF-16 surrogate pair`);
  }
}

function normalizeSelection(selection: Partial<SelectionRange> | undefined, fallback: number): SelectionRange {
  if (selection === undefined) {
    return { anchor: fallback, active: fallback };
  }
  const anchor = selection.anchor ?? fallback;
  const active = selection.active ?? anchor;
  assertNonNegativeInteger(anchor, 'selection.anchor');
  assertNonNegativeInteger(active, 'selection.active');
  return { anchor, active };
}

function assertSelectionInText(selection: SelectionRange, text: string): void {
  assertUtf16Boundary(text, selection.anchor, 'selection.anchor');
  assertUtf16Boundary(text, selection.active, 'selection.active');
}

function applyChange(text: string, change: Change): string {
  return text.slice(0, change.start) + change.text + text.slice(change.end);
}

function applyChanges(text: string, changes: readonly Change[]): string {
  return changes.reduce(applyChange, text);
}

function orderedRange(selection: SelectionRange): [number, number] {
  return [Math.min(selection.anchor, selection.active), Math.max(selection.anchor, selection.active)];
}

export class BranchingHistory {
  readonly maxHistoryNodes: number;
  private nodes = new Map<string, HistoryNode>();
  private roots = new Set<string>();
  private currentId: string;
  private nextSequence = 0;
  private coalesceId: string | null = null;
  private transactionChanges: Change[] | null = null;
  private transactionSelection: SelectionRange | null = null;

  constructor(options: { text?: string; selection?: Partial<SelectionRange>; maxHistoryNodes?: number } = {}) {
    const text = options.text ?? '';
    assertString(text, 'text');
    const selection = normalizeSelection(options.selection, text.length);
    assertSelectionInText(selection, text);
    const maxHistoryNodes = options.maxHistoryNodes ?? 200;
    assertNonNegativeInteger(maxHistoryNodes, 'maxHistoryNodes');
    if (maxHistoryNodes < 1) {
      throw new RangeError('maxHistoryNodes must be at least 1');
    }
    this.maxHistoryNodes = maxHistoryNodes;
    this.currentId = this.createId();
    this.nodes.set(this.currentId, {
      id: this.currentId,
      parentId: null,
      childIds: [],
      forward: [],
      selection,
      baselineText: text,
    });
    this.roots.add(this.currentId);
  }

  get text(): string {
    return this.getSnapshot().text;
  }

  get selection(): SelectionRange {
    return { ...this.getSnapshot().selection };
  }

  get currentNodeId(): string {
    return this.currentId;
  }

  get nodeCount(): number {
    return this.nodes.size;
  }

  get inTransaction(): boolean {
    return this.transactionChanges !== null;
  }

  private get currentNode(): HistoryNode {
    const node = this.nodes.get(this.currentId);
    if (!node) throw new Error(`Missing history node: ${this.currentId}`);
    return node;
  }

  private createId(): string {
    this.nextSequence += 1;
    return `n${this.nextSequence}`;
  }

  getSnapshot(): EditorSnapshot {
    const selection = this.transactionSelection ?? this.currentNode.selection;
    return { text: this.getWorkingText(), selection: { ...selection } };
  }

  private getWorkingText(): string {
    const baseText = this.resolveText(this.currentNode);
    return this.transactionChanges ? applyChanges(baseText, this.transactionChanges) : baseText;
  }

  private resolveText(node: HistoryNode): string {
    if (node.baselineText !== undefined) return node.baselineText;
    if (node.parentId === null) throw new Error(`Root node ${node.id} has no baseline text`);
    const parent = this.nodes.get(node.parentId);
    if (!parent) throw new Error(`Missing parent node: ${node.parentId}`);
    return applyChanges(this.resolveText(parent), node.forward);
  }

  setSelection(selection: Partial<SelectionRange>): void {
    const text = this.getWorkingText();
    if (this.transactionChanges) {
      this.transactionSelection = normalizeSelection(selection, text.length);
      assertSelectionInText(this.transactionSelection, text);
      return;
    }
    const next = normalizeSelection(selection, text.length);
    assertSelectionInText(next, text);
    this.currentNode.selection = next;
    this.coalesceId = null;
  }

  replace(
    start: number,
    end: number,
    text: string,
    postSelection?: Partial<SelectionRange>,
  ): EditorSnapshot {
    assertNonNegativeInteger(start, 'start');
    assertNonNegativeInteger(end, 'end');
    if (end < start) throw new RangeError('end must not be before start');
    assertString(text, 'text');
    const oldText = this.getWorkingText();
    assertUtf16Boundary(oldText, start, 'start');
    assertUtf16Boundary(oldText, end, 'end');

    if (this.transactionChanges) {
      this.recordTransactionChange(start, end, text, postSelection, oldText);
      return this.getSnapshot();
    }

    const replacementLength = end - start;
    if (replacementLength === 0 && text.length === 0) {
      if (postSelection) this.setSelection(postSelection);
      return this.getSnapshot();
    }

    const coalesceNode = this.findCoalesceNode(start, end, text, replacementLength);
    if (coalesceNode) {
      this.extendCoalescedNode(coalesceNode, start, text, postSelection, oldText);
      this.compactToCapacity();
      return this.getSnapshot();
    }

    const fallback = start + text.length;
    const selection = normalizeSelection(postSelection, fallback);
    this.addNode([{ start, end, text }], selection);
    this.coalesceId = replacementLength === 0 && text.length > 0 ? this.currentId : null;
    this.compactToCapacity();
    return this.getSnapshot();
  }

  private findCoalesceNode(
    start: number,
    end: number,
    text: string,
    replacementLength: number,
  ): HistoryNode | null {
    if (this.coalesceId !== this.currentId || replacementLength !== 0 || text.length === 0) {
      return null;
    }
    const node = this.currentNode;
    if (node.forward.length !== 1) return null;
    const forward = node.forward[0];
    if (forward.start + forward.text.length !== start) return null;
    const oldSelection = node.selection;
    const [oldFocusStart, oldFocusEnd] = orderedRange(oldSelection);
    if (oldFocusStart !== start || oldFocusEnd !== start) return null;
    return node;
  }

  private extendCoalescedNode(
    node: HistoryNode,
    start: number,
    text: string,
    postSelection: Partial<SelectionRange> | undefined,
    oldText: string,
  ): void {
    node.forward[0].end = start;
    node.forward[0].text += text;
    node.selection = normalizeSelection(postSelection, start + text.length);
    assertSelectionInText(node.selection, oldText + text);
  }

  private recordTransactionChange(
    start: number,
    end: number,
    text: string,
    postSelection: Partial<SelectionRange> | undefined,
    oldText: string,
  ): void {
    const changes = this.transactionChanges!;
    changes.push({ start, end, text });
    const fallback = start + text.length;
    this.transactionSelection = normalizeSelection(postSelection, fallback);
    assertSelectionInText(this.transactionSelection, applyChanges(oldText, [{ start, end, text }]));
  }

  beginTransaction(): void {
    if (this.transactionChanges) throw new Error('A transaction is already active');
    this.transactionChanges = [];
    this.transactionSelection = null;
    this.coalesceId = null;
  }

  commitTransaction(): EditorSnapshot {
    if (!this.transactionChanges) throw new Error('No active transaction');
    const changes = this.transactionChanges;
    const selection = this.transactionSelection ?? this.currentNode.selection;
    this.transactionChanges = null;
    this.transactionSelection = null;
    if (changes.length > 0) {
      this.addNode(changes, selection);
    } else {
      this.currentNode.selection = selection;
    }
    this.coalesceId = null;
    if (changes.length > 0) this.compactToCapacity();
    return this.getSnapshot();
  }

  rollbackTransaction(): void {
    if (!this.transactionChanges) throw new Error('No active transaction');
    this.transactionChanges = null;
    this.transactionSelection = null;
    this.coalesceId = null;
  }

  private addNode(forward: Change[], selection: SelectionRange): void {
    const id = this.createId();
    const parent = this.currentNode;
    const node: HistoryNode = {
      id,
      parentId: parent.id,
      childIds: [],
      forward,
      selection,
    };
    parent.childIds.push(id);
    this.nodes.set(id, node);
    this.currentId = id;
  }

  undo(): EditorSnapshot {
    if (this.transactionChanges) throw new Error('Commit or roll back the active transaction before undo');
    const current = this.currentNode;
    if (current.parentId !== null) {
      this.currentId = current.parentId;
    }
    this.coalesceId = null;
    return this.getSnapshot();
  }

  redo(childIndex = 0): EditorSnapshot {
    if (this.transactionChanges) throw new Error('Commit or roll back the active transaction before redo');
    const childId = this.currentNode.childIds[childIndex];
    if (childId === undefined) {
      throw new RangeError(`Child branch ${childIndex} does not exist`);
    }
    this.currentId = childId;
    this.coalesceId = null;
    return this.getSnapshot();
  }

  getBranches(): BranchSummary[] {
    return [...this.nodes.values()]
      .filter((node) => node.childIds.length === 0)
      .sort((left, right) => left.id.localeCompare(right.id, undefined, { numeric: true }))
      .map((node) => ({
        id: node.id,
        text: this.resolveText(node),
        selection: { ...node.selection },
      }));
  }

  switchTo(nodeId: string): EditorSnapshot {
    if (this.transactionChanges) throw new Error('Commit or roll back the active transaction before switching branches');
    const node = this.nodes.get(nodeId);
    if (!node) throw new RangeError(`Unknown history node: ${nodeId}`);
    this.currentId = nodeId;
    this.coalesceId = null;
    return this.getSnapshot();
  }

  switchToBranch(branchIndex: number): EditorSnapshot {
    assertNonNegativeInteger(branchIndex, 'branchIndex');
    const branch = this.getBranches()[branchIndex];
    if (!branch) throw new RangeError(`Branch ${branchIndex} does not exist`);
    return this.switchTo(branch.id);
  }

  replayVisibleNodes(): ReplayState[] {
    const result: ReplayState[] = [];
    const roots = [...this.roots].sort((left, right) =>
      left.localeCompare(right, undefined, { numeric: true }),
    );
    for (const rootId of roots) {
      const root = this.nodes.get(rootId);
      if (!root || root.baselineText === undefined) throw new Error(`Invalid compacted root: ${rootId}`);
      this.replayNode(root, root.baselineText, result);
    }
    return result;
  }

  private replayNode(node: HistoryNode, text: string, result: ReplayState[]): void {
    result.push({
      nodeId: node.id,
      parentId: node.parentId,
      text,
      selection: { ...node.selection },
    });
    for (const childId of node.childIds) {
      const child = this.nodes.get(childId);
      if (!child) throw new Error(`Missing child node: ${childId}`);
      this.replayNode(child, applyChanges(text, child.forward), result);
    }
  }

  compactToCapacity(): void {
    while (this.nodes.size > this.maxHistoryNodes) {
      const rootIds = [...this.roots].sort((left, right) =>
        left.localeCompare(right, undefined, { numeric: true }),
      );
      const root = rootIds
        .map((id) => this.nodes.get(id)!)
        .find((candidate) => candidate.childIds.length > 0 && candidate.id !== this.currentId);
      if (!root) return;

      for (const childId of [...root.childIds]) {
        const child = this.nodes.get(childId);
        if (!child) continue;
        child.baselineText = this.resolveText(child);
        child.forward = [];
        child.parentId = null;
        this.roots.add(childId);
      }
      root.childIds = [];
      this.roots.delete(root.id);
      this.nodes.delete(root.id);
      if (this.coalesceId === root.id) this.coalesceId = null;
    }
  }
}
