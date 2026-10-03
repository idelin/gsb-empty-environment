import { appendFileSync, existsSync, readFileSync, mkdirSync } from "node:fs";
import { dirname } from "node:path";
import type { AuditEvent } from "./types.ts";

/** Durable audit trail for events that require operator attention. */
export class AuditLog {
  private readonly path: string;

  constructor(path: string) {
    this.path = path;
    mkdirSync(dirname(path), { recursive: true });
  }

  record(event: AuditEvent): void {
    appendFileSync(this.path, JSON.stringify(event) + "\n", "utf8");
  }

  events(): AuditEvent[] {
    if (!existsSync(this.path)) return [];
    const raw = readFileSync(this.path, "utf8").trim();
    if (raw === "") return [];
    return raw.split("\n").map((line) => JSON.parse(line) as AuditEvent);
  }
}
