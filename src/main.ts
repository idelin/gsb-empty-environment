import { readFileSync } from "node:fs";
import { solveExact } from "./exact";
import { solveGreedy } from "./greedy";
import { buildModel } from "./model";
import { formatReport } from "./report";
import type { Instance } from "./types";

const DEMO: Instance = {
  budget: 12,
  changes: [
    { id: "A", cost: 3 },
    { id: "B", cost: 2, requires: ["A"] },
    { id: "C", cost: 4, conflicts: ["B"] },
    { id: "D", cost: 5 },
    { id: "E", cost: 1, requires: ["D"] },
    { id: "F", cost: 2 },
    { id: "G", cost: 3, conflicts: ["F"] },
    { id: "H", cost: 8 },
    { id: "I", cost: 2, requires: ["H"] },
    { id: "J", cost: 2 },
  ],
};

function loadInstance(argv: string[]): Instance {
  const path = argv.find((a) => !a.startsWith("-"));
  if (!path) return DEMO;
  const raw = JSON.parse(readFileSync(path, "utf8")) as Instance;
  return raw;
}

function main(): void {
  const instance = loadInstance(process.argv.slice(2));
  const model = buildModel(instance);
  const exact = solveExact(model, instance.budget);
  const greedy = solveGreedy(model, instance.budget);
  console.log(formatReport(model, instance.budget, exact, greedy));
}

main();
