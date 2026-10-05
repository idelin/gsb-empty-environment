// Minimal ambient declarations so the tests type-check without @types/node.
declare const console: {
  log(...args: unknown[]): void;
  error(...args: unknown[]): void;
};
declare const process: {
  exitCode: number;
};
