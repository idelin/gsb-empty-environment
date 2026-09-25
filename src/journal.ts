import type { DecisionRecord } from './types.ts';

export class Journal {
  private readonly records: DecisionRecord[] = [];
  private clock = 0;

  // Atomic commit: the complete record (decision + access evidence) is
  // appended in a single synchronous step. Observers never see a partially
  // written record, and cancelled/failed evaluations commit nothing.
  commit(input: Omit<DecisionRecord, 'seq'>): DecisionRecord {
    const record: DecisionRecord = Object.freeze({
      ...input,
      evidence: Object.freeze(input.evidence.map((e) => Object.freeze({ ...e }))),
      seq: ++this.clock,
    });
    this.records.push(record);
    return record;
  }

  all(): readonly DecisionRecord[] {
    return [...this.records];
  }
}
