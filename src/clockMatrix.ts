import type { KnowledgeVector } from "./types.ts";

export class ClockMatrix {
  private rows = new Map<string, Map<string, number>>();

  observe(nodeId: string, vector: KnowledgeVector): void {
    let row = this.rows.get(nodeId);
    if (!row) {
      row = new Map();
      this.rows.set(nodeId, row);
    }
    for (const [id, clock] of Object.entries(vector)) {
      const known = row.get(id);
      if (known === undefined || clock > known) row.set(id, clock);
    }
  }

  frontier(): Map<string, number> {
    const result = new Map<string, number>();
    for (const row of this.rows.values()) {
      for (const [id, clock] of row) {
        const current = result.get(id);
        if (current === undefined || clock < current) result.set(id, clock);
      }
    }
    return result;
  }

  vectorOf(nodeId: string): KnowledgeVector {
    const row = this.rows.get(nodeId);
    const vector: KnowledgeVector = {};
    if (row) for (const [id, clock] of row) vector[id] = clock;
    return vector;
  }

  nodeIds(): string[] {
    return [...this.rows.keys()];
  }
}
