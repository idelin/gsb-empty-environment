/**
 * MemoryMonitor: observes GC activity and heap usage during execution.
 *
 * - Subscribes to Node's `gc` performance entries to count GC cycles and
 *   total pause time (zero events => zero GC pauses).
 * - Samples `heapUsed` to track the heap envelope (flat curve => no
 *   allocation pressure).
 */

import { PerformanceObserver } from 'node:perf_hooks';

export interface MemoryReport {
  readonly gcEvents: number;
  readonly gcPauseMs: number;
  readonly baselineHeapBytes: number;
  readonly minHeapBytes: number;
  readonly maxHeapBytes: number;
  readonly heapDeltaBytes: number;
  readonly samples: number;
}

export class MemoryMonitor {
  private observer: PerformanceObserver | null = null;
  private gcEvents = 0;
  private gcPauseMs = 0;
  private baselineHeap = 0;
  private minHeap = 0;
  private maxHeap = 0;
  private samples = 0;

  start(): void {
    this.gcEvents = 0;
    this.gcPauseMs = 0;
    try {
      this.observer = new PerformanceObserver((list) => {
        for (const entry of list.getEntries()) {
          this.gcEvents++;
          this.gcPauseMs += entry.duration;
        }
      });
      this.observer.observe({ entryTypes: ['gc'] });
    } catch {
      this.observer = null; // gc entryType unsupported: fall back to heap envelope
    }
    const heap = process.memoryUsage().heapUsed;
    this.baselineHeap = heap;
    this.minHeap = heap;
    this.maxHeap = heap;
    this.samples = 1;
  }

  sample(): void {
    const heap = process.memoryUsage().heapUsed;
    if (heap < this.minHeap) this.minHeap = heap;
    if (heap > this.maxHeap) this.maxHeap = heap;
    this.samples++;
  }

  stop(): MemoryReport {
    this.sample();
    if (this.observer !== null) {
      this.observer.disconnect();
      this.observer = null;
    }
    return {
      gcEvents: this.gcEvents,
      gcPauseMs: this.gcPauseMs,
      baselineHeapBytes: this.baselineHeap,
      minHeapBytes: this.minHeap,
      maxHeapBytes: this.maxHeap,
      heapDeltaBytes: this.maxHeap - this.baselineHeap,
      samples: this.samples,
    };
  }
}
