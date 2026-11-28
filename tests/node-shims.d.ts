/**
 * Minimal ambient declarations for the Node globals used by the zero-dependency
 * test harness. The full @types/node package is unavailable in this offline
 * environment; the runtime implements these natively.
 */

interface Console {
  log(...data: unknown[]): void;
}

declare const console: Console;

declare namespace NodeJS {
  interface Process {
    exitCode: number;
  }
}

declare const process: NodeJS.Process;
