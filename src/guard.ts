export type CausalAction = 'writes' | 'notifies';

export interface CausalEntry {
  readonly from: string;
  readonly to: string;
  readonly action: CausalAction;
}

export type LoopReason = 'iteration-threshold' | 'call-stack-cycle';

export interface LoopReport {
  readonly reason: LoopReason;
  readonly iterations: number;
  readonly threshold: number;
  readonly triggerSource: string;
  readonly cycle: readonly string[];
  readonly chain: readonly CausalEntry[];
}

export class CircularSignalUpdate extends Error {
  override readonly name = 'CircularSignalUpdate';
  readonly report: LoopReport;

  constructor(report: LoopReport) {
    super(formatLoopReport(report));
    this.report = report;
  }
}

export function formatLoopReport(report: LoopReport): string {
  const lines: string[] = [];
  lines.push(`CircularSignalUpdate: reactive loop blocked (reason: ${report.reason})`);
  lines.push(`iterations: ${report.iterations} (threshold: ${report.threshold})`);
  lines.push(`trigger source: ${report.triggerSource}`);
  lines.push(`cycle: ${report.cycle.join(' -> ')}`);
  if (report.chain.length > 0) {
    const tail = report.chain.slice(-8);
    lines.push(`causal chain (last ${tail.length} of ${report.chain.length} events):`);
    for (const entry of tail) {
      lines.push(`  ${entry.from} --${entry.action}--> ${entry.to}`);
    }
  }
  return lines.join('\n');
}

function extractCycle(chain: readonly CausalEntry[], trigger: string): string[] {
  const seq: string[] = [];
  for (const entry of chain) {
    if (seq.length === 0 || seq[seq.length - 1] !== entry.from) seq.push(entry.from);
    if (seq[seq.length - 1] !== entry.to) seq.push(entry.to);
  }
  if (seq.length === 0) return [trigger, trigger];
  const last = seq[seq.length - 1];
  const prev = seq.lastIndexOf(last, seq.length - 2);
  return seq.slice(prev === -1 ? 0 : prev);
}

export class ReactiveLoopGuard {
  readonly maxIterations: number;
  private iterations = 0;
  private epochDepth = 0;
  private chain: CausalEntry[] = [];
  private readonly executionStack: string[] = [];

  constructor(maxIterations = 50) {
    if (!Number.isInteger(maxIterations) || maxIterations < 1) {
      throw new RangeError('maxIterations must be a positive integer');
    }
    this.maxIterations = maxIterations;
  }

  get currentIterations(): number {
    return this.iterations;
  }

  beginEpoch(): void {
    if (this.epochDepth === 0) {
      this.iterations = 0;
      this.chain = [];
    }
    this.epochDepth += 1;
  }

  endEpoch(): void {
    this.epochDepth -= 1;
    if (this.epochDepth === 0) {
      this.iterations = 0;
      this.chain = [];
    }
  }

  record(from: string, to: string, action: CausalAction): void {
    if (this.epochDepth > 0) {
      this.chain.push({ from, to, action });
    }
  }

  enterNode(label: string): void {
    const idx = this.executionStack.indexOf(label);
    if (idx !== -1) {
      throw new CircularSignalUpdate({
        reason: 'call-stack-cycle',
        iterations: this.iterations,
        threshold: this.maxIterations,
        triggerSource: label,
        cycle: [...this.executionStack.slice(idx), label],
        chain: [...this.chain],
      });
    }
    this.executionStack.push(label);
  }

  exitNode(label: string): void {
    const idx = this.executionStack.lastIndexOf(label);
    if (idx !== -1) this.executionStack.splice(idx, 1);
  }

  recordIteration(nodeLabel: string): void {
    if (this.epochDepth === 0) return;
    this.iterations += 1;
    if (this.iterations >= this.maxIterations) {
      throw new CircularSignalUpdate({
        reason: 'iteration-threshold',
        iterations: this.iterations,
        threshold: this.maxIterations,
        triggerSource: nodeLabel,
        cycle: extractCycle(this.chain, nodeLabel),
        chain: [...this.chain],
      });
    }
  }
}
