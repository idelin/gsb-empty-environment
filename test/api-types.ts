import { executeMerge, update } from "../src/index.js";

interface TargetRow {
  id: number;
  name: string;
  active: boolean;
}

interface SourceRow {
  id: number;
  newName: string;
}

// This file is compile-only. It verifies that target and source may have
// different shapes while on/condition/assignment callbacks remain typed.
const result = executeMerge({
  target: [{ id: 1, name: "old", active: true }] satisfies TargetRow[],
  source: [{ id: 1, newName: "new" }] satisfies SourceRow[],
  on: (target, source) => target.id === source.id,
  whenMatched: [
    {
      condition: (_target, source) => source.newName.length > 0,
      action: update({
        name: (_target, source) => source.newName,
        active: true,
      }),
    },
  ],
});

void result;
