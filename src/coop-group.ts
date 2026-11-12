export interface DocumentRef {
  origin: string;
  policy: string;
}

export interface CompatibilityRule {
  crossOrigin?: boolean;
}

export type CompatibilityMatrix = Record<
  string,
  Record<string, boolean | CompatibilityRule>
>;

export function isCompatible(
  a: DocumentRef,
  b: DocumentRef,
  matrix: CompatibilityMatrix,
): boolean {
  const rule = matrix[a.policy]?.[b.policy];
  if (rule === undefined || rule === false) return false;
  if (a.origin === b.origin) return true;
  return typeof rule === "object" && rule.crossOrigin === true;
}

export interface MessageRecord {
  id: number;
  from: string;
  to: string;
  payload: unknown;
  groupId: number;
  toDocumentSeq: number;
}

export type NavigationCause = "navigate" | "back" | "forward";

interface WindowState {
  id: string;
  document: DocumentRef;
  documentSeq: number;
  groupId: number;
  links: Set<string>;
}

export class CoopGroupManager {
  private readonly matrix: CompatibilityMatrix;
  private readonly windows = new Map<string, WindowState>();
  private readonly queue: Array<() => unknown> = [];
  private nextGroup = 1;
  private nextDocument = 1;
  private nextMessage = 1;
  readonly messages: MessageRecord[] = [];

  constructor(matrix: CompatibilityMatrix) {
    this.matrix = matrix;
  }

  createWindow(
    id: string,
    document: DocumentRef,
    openerId?: string,
  ): { groupId: number; documentSeq: number } {
    if (this.windows.has(id)) throw new Error(`duplicate window: ${id}`);
    const state: WindowState = {
      id,
      document,
      documentSeq: this.nextDocument++,
      groupId: this.nextGroup++,
      links: new Set(),
    };
    this.windows.set(id, state);
    if (openerId !== undefined) {
      const opener = this.requireWindow(openerId);
      const members = this.membersOf(opener.groupId);
      if (members.every((m) => isCompatible(document, m.document, this.matrix))) {
        state.groupId = opener.groupId;
        state.links.add(openerId);
        opener.links.add(id);
      }
    }
    return { groupId: state.groupId, documentSeq: state.documentSeq };
  }

  navigate(
    id: string,
    document: DocumentRef,
    cause: NavigationCause = "navigate",
  ): { keptGroup: boolean; groupId: number; documentSeq: number } {
    void cause;
    const state = this.requireWindow(id);
    const members = this.membersOf(state.groupId, id);
    const keep = members.every((m) =>
      isCompatible(document, m.document, this.matrix),
    );
    state.document = document;
    state.documentSeq = this.nextDocument++;
    if (!keep) {
      for (const neighborId of state.links) {
        this.requireWindow(neighborId).links.delete(id);
      }
      state.links.clear();
      state.groupId = this.nextGroup++;
    }
    return {
      keptGroup: keep,
      groupId: state.groupId,
      documentSeq: state.documentSeq,
    };
  }

  canAccess(fromId: string, toId: string): boolean {
    const from = this.windows.get(fromId);
    const to = this.windows.get(toId);
    if (from === undefined || to === undefined) return false;
    return from.groupId === to.groupId && from.links.has(toId);
  }

  postMessage(fromId: string, toId: string, payload: unknown): MessageRecord | null {
    if (!this.canAccess(fromId, toId)) return null;
    const receiver = this.requireWindow(toId);
    const record: MessageRecord = {
      id: this.nextMessage++,
      from: fromId,
      to: toId,
      payload,
      groupId: receiver.groupId,
      toDocumentSeq: receiver.documentSeq,
    };
    this.messages.push(record);
    return record;
  }

  defer(task: () => unknown): void {
    this.queue.push(task);
  }

  flush(): unknown[] {
    const results: unknown[] = [];
    while (this.queue.length > 0) {
      results.push(this.queue.shift()!());
    }
    return results;
  }

  groupOf(id: string): number {
    return this.requireWindow(id).groupId;
  }

  linksOf(id: string): string[] {
    return [...this.requireWindow(id).links];
  }

  private membersOf(groupId: number, excludeId?: string): WindowState[] {
    return [...this.windows.values()].filter(
      (w) => w.groupId === groupId && w.id !== excludeId,
    );
  }

  private requireWindow(id: string): WindowState {
    const state = this.windows.get(id);
    if (state === undefined) throw new Error(`unknown window: ${id}`);
    return state;
  }
}
