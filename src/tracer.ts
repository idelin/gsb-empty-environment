import * as fs from "fs";
import { normalizeAbs } from "./fspaths";
import { resolveChain } from "./resolve";
import { AccessLog, AccessRecord, Workload, WorkloadOp } from "./types";

/**
 * Executable checker: performs each workload operation for real and records
 * the resulting access, including the full symlink expansion chain.
 */
export function runWorkload(workload: Workload, base: string): AccessLog {
  const records: AccessRecord[] = [];
  for (const op of workload.ops) {
    records.push(performOp(op, base));
  }
  return {
    tool: "access-audit",
    base,
    startedAt: new Date().toISOString(),
    records,
  };
}

function performOp(op: WorkloadOp, base: string): AccessRecord {
  const abs = normalizeAbs(op.path, base);
  const resolved = resolveChain(abs);
  const record: AccessRecord = { op: op.op, path: abs, ...resolved };

  // Perform the actual access so the check reflects real IO, not just
  // metadata. Failures are captured on the record, never thrown.
  try {
    switch (op.op) {
      case "readFile":
        fs.readFileSync(abs);
        break;
      case "readdir":
        fs.readdirSync(abs);
        break;
      case "stat":
        fs.statSync(abs);
        break;
      case "exists":
        fs.accessSync(abs, fs.constants.F_OK);
        break;
      default:
        throw new Error(`unknown op: ${(op as WorkloadOp).op}`);
    }
  } catch (err) {
    const code = (err as NodeJS.ErrnoException).code;
    if (code && !record.error) {
      record.error = code;
      if (code === "ENOENT" || code === "ENOTDIR") {
        record.exists = false;
      }
    } else if (!record.error) {
      record.error = String(err);
    }
  }
  return record;
}
