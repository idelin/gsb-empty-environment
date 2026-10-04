/**
 * A min-priority queue with stableable handles.
 *
 * Features:
 * - Handles are `{ slot, generation }` pairs. When a slot is recycled after
 *   cancellation/extraction, its generation is bumped, so every stale handle
 *   is detected and rejected.
 * - Ties on priority are broken by insertion sequence number (FIFO). The
 *   sequence counter is a `bigint`, so it stays exact beyond
 *   `Number.MAX_SAFE_INTEGER`.
 * - `batchSetPriority` is atomic: on any failure it rolls back both the heap
 *   array and the slot -> heap-index map.
 */

export interface Handle {
  readonly slot: number;
  readonly generation: number;
}

export interface QueueEntry {
  readonly priority: number;
  readonly seq: bigint;
}

export interface BatchUpdate {
  readonly handle: Handle;
  readonly priority: number;
}

export interface PriorityQueueOptions {
  /** Starting value of the insertion sequence counter (mainly for tests). */
  readonly initialSeq?: bigint | number;
}

export class StaleHandleError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "StaleHandleError";
  }
}

interface Slot {
  generation: number;
  active: boolean;
  priority: number;
  seq: bigint;
  heapIndex: number;
}

function assertValidPriority(priority: number): void {
  if (typeof priority !== "number" || Number.isNaN(priority)) {
    throw new TypeError(`priority must be a number and not NaN, got ${String(priority)}`);
  }
}

export class PriorityQueue {
  private readonly slots: Slot[] = [];
  private readonly freeList: number[] = [];
  private heap: number[] = [];
  private nextSeq: bigint;

  constructor(options?: PriorityQueueOptions) {
    const initial = options?.initialSeq ?? 0n;
    this.nextSeq = typeof initial === "bigint" ? initial : BigInt(initial);
  }

  get size(): number {
    return this.heap.length;
  }

  /** True when the handle refers to a live entry. */
  has(handle: Handle): boolean {
    return this.lookupSlot(handle) !== undefined;
  }

  insert(priority: number): Handle {
    assertValidPriority(priority);
    const seq = this.nextSeq;
    this.nextSeq += 1n;

    let slotIndex: number;
    const reused = this.freeList.pop();
    if (reused === undefined) {
      slotIndex = this.slots.length;
      this.slots.push({ generation: 0, active: true, priority, seq, heapIndex: -1 });
    } else {
      slotIndex = reused;
      const slot = this.slots[slotIndex];
      slot.active = true;
      slot.priority = priority;
      slot.seq = seq;
      slot.heapIndex = -1;
    }

    const slot = this.slots[slotIndex];
    slot.heapIndex = this.heap.length;
    this.heap.push(slotIndex);
    this.siftUp(slot.heapIndex);
    return { slot: slotIndex, generation: slot.generation };
  }

  peek(): QueueEntry | undefined {
    if (this.heap.length === 0) return undefined;
    const top = this.slots[this.heap[0]];
    return { priority: top.priority, seq: top.seq };
  }

  extractMin(): QueueEntry | undefined {
    if (this.heap.length === 0) return undefined;
    const top = this.slots[this.heap[0]];
    const entry: QueueEntry = { priority: top.priority, seq: top.seq };
    this.removeAt(0);
    return entry;
  }

  /**
   * Remove the entry referenced by `handle`.
   * Returns false (instead of throwing) for stale handles so that duplicate
   * cancellation is safe.
   */
  cancel(handle: Handle): boolean {
    const slot = this.lookupSlot(handle);
    if (slot === undefined) return false;
    this.removeAt(slot.heapIndex);
    return true;
  }

  /** Change the priority of a live entry. Throws StaleHandleError for stale handles. */
  setPriority(handle: Handle, priority: number): void {
    assertValidPriority(priority);
    const slot = this.requireSlot(handle);
    slot.priority = priority;
    this.siftUp(slot.heapIndex);
    this.siftDown(slot.heapIndex);
  }

  /**
   * Apply several priority changes atomically.
   *
   * If any update is invalid (stale handle or bad priority), every change
   * made so far is rolled back: old priorities are restored, the heap array
   * is restored, and the slot -> heap-index map is rebuilt from the restored
   * heap so handles keep resolving to the right positions.
   */
  batchSetPriority(updates: ReadonlyArray<BatchUpdate>): void {
    const savedHeap = this.heap.slice();
    const savedPriorities = new Map<number, number>();
    try {
      for (const update of updates) {
        assertValidPriority(update.priority);
        const slot = this.requireSlot(update.handle);
        if (!savedPriorities.has(update.handle.slot)) {
          savedPriorities.set(update.handle.slot, slot.priority);
        }
        slot.priority = update.priority;
        this.siftUp(slot.heapIndex);
        this.siftDown(slot.heapIndex);
      }
    } catch (error) {
      for (const [slotIndex, priority] of savedPriorities) {
        this.slots[slotIndex].priority = priority;
      }
      this.heap = savedHeap;
      for (let i = 0; i < this.heap.length; i++) {
        this.slots[this.heap[i]].heapIndex = i;
      }
      throw error;
    }
  }

  /**
   * Internal consistency check, exposed for tests and debugging.
   * Throws when the heap property or the slot/heap index bijection is broken.
   */
  debugCheckInvariants(): void {
    for (let i = 0; i < this.heap.length; i++) {
      const slotIndex = this.heap[i];
      const slot = this.slots[slotIndex];
      if (slot === undefined || !slot.active) {
        throw new Error(`heap[${i}] references inactive slot ${slotIndex}`);
      }
      if (slot.heapIndex !== i) {
        throw new Error(
          `index map broken: heap[${i}] = slot ${slotIndex} but slot.heapIndex = ${slot.heapIndex}`,
        );
      }
      const left = 2 * i + 1;
      const right = 2 * i + 2;
      if (left < this.heap.length && this.lessSlotIndices(left, i)) {
        throw new Error(`heap property violated between index ${i} and ${left}`);
      }
      if (right < this.heap.length && this.lessSlotIndices(right, i)) {
        throw new Error(`heap property violated between index ${i} and ${right}`);
      }
    }
    let activeCount = 0;
    for (const slot of this.slots) {
      if (slot.active) activeCount += 1;
    }
    if (activeCount !== this.heap.length) {
      throw new Error(`active slot count ${activeCount} != heap size ${this.heap.length}`);
    }
    const seen = new Set(this.heap);
    if (seen.size !== this.heap.length) {
      throw new Error("heap contains duplicate slot references");
    }
  }

  private lookupSlot(handle: Handle): Slot | undefined {
    if (handle === null || typeof handle !== "object") return undefined;
    const slot = this.slots[handle.slot];
    if (slot === undefined || !slot.active || slot.generation !== handle.generation) {
      return undefined;
    }
    return slot;
  }

  private requireSlot(handle: Handle): Slot {
    const slot = this.lookupSlot(handle);
    if (slot === undefined) {
      throw new StaleHandleError(
        `stale handle: slot=${String(handle?.slot)} generation=${String(handle?.generation)}`,
      );
    }
    return slot;
  }

  private removeAt(index: number): void {
    const removedSlotIndex = this.heap[index];
    const lastSlotIndex = this.heap.pop() as number;
    if (index < this.heap.length) {
      this.heap[index] = lastSlotIndex;
      this.slots[lastSlotIndex].heapIndex = index;
      this.siftUp(index);
      this.siftDown(this.slots[lastSlotIndex].heapIndex);
    }
    const slot = this.slots[removedSlotIndex];
    slot.active = false;
    slot.heapIndex = -1;
    slot.generation += 1;
    this.freeList.push(removedSlotIndex);
  }

  private lessSlotIndices(i: number, j: number): boolean {
    const a = this.slots[this.heap[i]];
    const b = this.slots[this.heap[j]];
    if (a.priority !== b.priority) return a.priority < b.priority;
    return a.seq < b.seq;
  }

  private swapHeap(i: number, j: number): void {
    const tmp = this.heap[i];
    this.heap[i] = this.heap[j];
    this.heap[j] = tmp;
    this.slots[this.heap[i]].heapIndex = i;
    this.slots[this.heap[j]].heapIndex = j;
  }

  private siftUp(index: number): void {
    let child = index;
    while (child > 0) {
      const parent = (child - 1) >> 1;
      if (!this.lessSlotIndices(child, parent)) break;
      this.swapHeap(child, parent);
      child = parent;
    }
  }

  private siftDown(index: number): void {
    let parent = index;
    for (;;) {
      const left = 2 * parent + 1;
      const right = left + 1;
      let smallest = parent;
      if (left < this.heap.length && this.lessSlotIndices(left, smallest)) smallest = left;
      if (right < this.heap.length && this.lessSlotIndices(right, smallest)) smallest = right;
      if (smallest === parent) break;
      this.swapHeap(parent, smallest);
      parent = smallest;
    }
  }
}
