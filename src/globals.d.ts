// Minimal ambient declarations so `tsc --noEmit` works without @types/node.
declare const console: {
  log(...args: unknown[]): void;
  error(...args: unknown[]): void;
};
declare const process: {
  exit(code?: number): void;
};
