/**
 * TaskPriorityQueue — a min-priority queue with stable, revocable handles.
 *
 * Semantics:
 * - Each enqueued item is addressed by a `Handle` carrying a `slot` index and
 *   a `generation` counter. Slots are recycled; every recycle bumps the
 *   generation, so any handle issued before the recycle is stale and rejected.
 * - Ordering is `(priority, seq)` lexicographic: ties on priority are broken
 *   by a monotonically increasing insertion sequence number (FIFO). The
 *   sequence is a `bigint`, so it stays exact beyond `Number.MAX_SAFE_INTEGER`.
 * - `updateMany` is atomic: if any update fails (stale handle or invalid
 *   priority), every earlier update of the same batch is rolled back —
 *   including the slot -> heap-index mapping, not just the heap array.
 */

export interface Handle {
  readonly slot: number;
  readonly generation: number;
}

export class StaleHandleError extends Error {
  constructor(message: string = "handle is stale or invalid") {
    super(message);
    this.name = "StaleHandleError";
  }
}

export interface DequeuedItem<T> {
  readonly value: T;
  readonly priority: number;
}

export interface BatchUpdate {
  readonly handle: Handle;
  readonly priority: number;
}

export interface TaskPriorityQueueOptions {
  /**
   * Starting value for the insertion sequence counter. Useful to resume from
   * a persisted state, and to operate beyond Number.MAX_SAFE_INTEGER.
   */
  readonly initialSeq?: bigint;
}

interface Entry<T> {
  slot: number;
  priority: number;
  seq: bigint;
  value: T;
}

interface SlotState {
  generation: number;
  /** Index into the heap array, or -1 when the slot is free. */
  heapIndex: number;
}

function assertValidPriority(priority: number): void {
  if (typeof priority !== "number" || !Number.isFinite(priority)) {
    throw new TypeError(`priority must be a finite number, got ${String(priority)}`);
  }
}

function comesBefore<T>(a: Entry<T>, b: Entry<T>): boolean {
  if (a.priority !== b.priority) return a.priority < b.priority;
  return a.seq < b.seq;
}

export class TaskPriorityQueue<T> {
  private heap: Array<Entry<T>> = [];
  private slots: Array<SlotState> = [];
  private freeSlots: Array<number> = [];
  private nextSeq: bigint;

  constructor(options?: TaskPriorityQueueOptions) {
    this.nextSeq = options?.initialSeq !== undefined ? BigInt(options.initialSeq) : 0n;
  }

  get size(): number {
    return this.heap.length;
  }

  get isEmpty(): boolean {
    return this.heap.length === 0;
  }

  /** Enqueue a value and return its handle. O(log n). */
  enqueue(value: T, priority: number): Handle {
    assertValidPriority(priority);

    let slotIndex = this.freeSlots.pop();
    if (slotIndex === undefined) {
      slotIndex = this.slots.length;
      this.slots.push({ generation: 0, heapIndex: -1 });
    }
    const slot = this.slots[slotIndex];

    const entry: Entry<T> = { slot: slotIndex, priority, seq: this.nextSeq, value };
    this.nextSeq += 1n;

    this.heap.push(entry);
    slot.heapIndex = this.heap.length - 1;
    this.siftUp(slot.heapIndex);

    return { slot: slotIndex, generation: slot.generation };
  }

  /** Inspect the minimal element without removing it. O(1). */
  peekMin(): DequeuedItem<T> | undefined {
    const top = this.heap[0];
    return top === undefined ? undefined : { value: top.value, priority: top.priority };
  }

  /** Remove and return the minimal element. O(log n). */
  extractMin(): DequeuedItem<T> | undefined {
    const top = this.heap[0];
    if (top === undefined) return undefined;
    this.removeAt(0);
    return { value: top.value, priority: top.priority };
  }

  /**
   * Cancel the item identified by `handle`.
   * Returns true if the item was present and removed; false if the handle is
   * stale (already cancelled/extracted, or its slot has been recycled).
   * O(log n).
   */
  cancel(handle: Handle): boolean {
    const state = this.resolveSlot(handle);
    if (state === undefined) return false;
    this.removeAt(state.heapIndex);
    return true;
  }

  /**
   * Change the priority of a live item. O(log n).
   * Throws StaleHandleError if the handle is stale, TypeError on an invalid priority.
   */
  changePriority(handle: Handle, priority: number): void {
    const index = this.requireIndex(handle);
    assertValidPriority(priority);
    this.heap[index].priority = priority;
    this.reheapify(index);
  }

  /**
   * Atomically apply a batch of priority updates.
   *
   * Updates are applied in order. If any update fails (stale handle or
   * invalid priority), the queue is restored to its exact prior state —
   * heap array, entry priorities, and the slot -> heap-index mapping — and
   * the error is rethrown. O(k log n) for k updates.
   */
  updateMany(updates: ReadonlyArray<BatchUpdate>): void {
    const savedHeap = this.heap.slice();
    const savedPriorities = this.heap.map((entry) => entry.priority);
    const savedHeapIndices = this.slots.map((slot) => slot.heapIndex);

    try {
      for (const update of updates) {
        const index = this.requireIndex(update.handle);
        assertValidPriority(update.priority);
        this.heap[index].priority = update.priority;
        this.reheapify(index);
      }
    } catch (error) {
      this.heap = savedHeap;
      for (let i = 0; i < savedHeap.length; i += 1) {
        savedHeap[i].priority = savedPriorities[i];
      }
      for (let slot = 0; slot < this.slots.length; slot += 1) {
        this.slots[slot].heapIndex = savedHeapIndices[slot];
      }
      throw error;
    }
  }

  /** Look up a handle, returning its slot state iff the handle is live. */
  private resolveSlot(handle: Handle): SlotState | undefined {
    if (handle === null || typeof handle !== "object") return undefined;
    const { slot, generation } = handle;
    if (!Number.isInteger(slot) || slot < 0 || slot >= this.slots.length) return undefined;
    const state = this.slots[slot];
    if (state.generation !== generation || state.heapIndex < 0) return undefined;
    return state;
  }

  private requireIndex(handle: Handle): number {
    const state = this.resolveSlot(handle);
    if (state === undefined) {
      throw new StaleHandleError(
        `stale handle (slot=${String(handle?.slot)}, generation=${String(handle?.generation)})`,
      );
    }
    return state.heapIndex;
  }

  /** Remove the entry at `index`, freeing its slot for reuse. */
  private removeAt(index: number): void {
    const removed = this.heap[index];
    const last = this.heap.pop()!;
    if (index < this.heap.length) {
      this.heap[index] = last;
      this.slots[last.slot].heapIndex = index;
      this.reheapify(index);
    }
    const freed = this.slots[removed.slot];
    freed.heapIndex = -1;
    freed.generation += 1;
    this.freeSlots.push(removed.slot);
  }

  private reheapify(index: number): void {
    const parent = (index - 1) >> 1;
    if (index > 0 && comesBefore(this.heap[index], this.heap[parent])) {
      this.siftUp(index);
    } else {
      this.siftDown(index);
    }
  }

  private siftUp(start: number): void {
    let index = start;
    while (index > 0) {
      const parent = (index - 1) >> 1;
      if (!comesBefore(this.heap[index], this.heap[parent])) return;
      this.swap(index, parent);
      index = parent;
    }
  }

  private siftDown(start: number): void {
    let index = start;
    const size = this.heap.length;
    for (;;) {
      const left = 2 * index + 1;
      const right = left + 1;
      let smallest = index;
      if (left < size && comesBefore(this.heap[left], this.heap[smallest])) smallest = left;
      if (right < size && comesBefore(this.heap[right], this.heap[smallest])) smallest = right;
      if (smallest === index) return;
      this.swap(index, smallest);
      index = smallest;
    }
  }

  private swap(i: number, j: number): void {
    const tmp = this.heap[i];
    this.heap[i] = this.heap[j];
    this.heap[j] = tmp;
    this.slots[this.heap[i].slot].heapIndex = i;
    this.slots[this.heap[j].slot].heapIndex = j;
  }
}
