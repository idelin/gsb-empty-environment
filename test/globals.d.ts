// Minimal ambient declarations so the project compiles without @types/node.
declare const console: {
  log(...args: unknown[]): void;
  error(...args: unknown[]): void;
};
declare const process: {
  exitCode: number;
};
