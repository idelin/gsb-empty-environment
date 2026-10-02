import { AtomicRef } from './atomic.ts';

class Node<T> {
  readonly value: T | null;
  readonly next = new AtomicRef<Node<T> | null>(null);

  constructor(value: T | null) {
    this.value = value;
  }
}

/**
 * Michael–Scott unbounded MPMC lock-free queue.
 *
 * All synchronization is done with compare-and-swap on the head/tail
 * pointers — there is no mutex anywhere, so enqueuers and dequeuers never
 * block each other (a failing CAS simply retries).
 */
export class LockFreeQueue<T> {
  private readonly head: AtomicRef<Node<T>>;
  private readonly tail: AtomicRef<Node<T>>;
  private count = 0;

  constructor() {
    const sentinel = new Node<T>(null);
    this.head = new AtomicRef(sentinel);
    this.tail = new AtomicRef(sentinel);
  }

  enqueue(value: T): void {
    const node = new Node<T>(value);
    for (;;) {
      const tail = this.tail.load();
      const next = tail.next.load();
      if (tail !== this.tail.load()) continue; // tail changed, retry
      if (next === null) {
        // Try to link the new node at the end of the list.
        if (tail.next.cas(null, node)) {
          // Linked: swing the tail (best effort, others may help).
          this.tail.cas(tail, node);
          this.count++;
          return;
        }
      } else {
        // Tail was lagging behind; help advance it.
        this.tail.cas(tail, next);
      }
    }
  }

  /** Returns undefined when the queue is empty. */
  dequeue(): T | undefined {
    for (;;) {
      const head = this.head.load();
      const tail = this.tail.load();
      const next = head.next.load();
      if (head !== this.head.load()) continue; // head changed, retry
      if (head === tail) {
        if (next === null) return undefined; // genuinely empty
        this.tail.cas(tail, next); // tail lagging; help advance it
      } else {
        const n = next as Node<T>;
        const value = n.value as T;
        // Swing the head; the old sentinel becomes garbage.
        if (this.head.cas(head, n)) {
          this.count--;
          return value;
        }
      }
    }
  }

  get size(): number {
    return this.count;
  }

  get isEmpty(): boolean {
    return this.count === 0;
  }

  /**
   * Structural invariant used by tests to prove no node leaks: a fully
   * drained queue collapses back to a single sentinel node.
   */
  get isCollapsedToSentinel(): boolean {
    const head = this.head.load();
    return head === this.tail.load() && head.next.load() === null;
  }
}
