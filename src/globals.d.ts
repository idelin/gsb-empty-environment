// Minimal ambient globals (offline environment: no @types/node).
declare const console: {
  log(...args: unknown[]): void;
  error(...args: unknown[]): void;
};
declare const process: {
  exit(code?: number): never;
  env: Record<string, string | undefined>;
};
