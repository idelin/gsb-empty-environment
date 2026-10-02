// Minimal ambient declarations so the project compiles without @types/node
// (offline environment). Only the APIs actually used are declared.
declare function require(id: string): any;
declare const console: {
  log(...args: any[]): void;
  error(...args: any[]): void;
};
declare const process: {
  exitCode: number | undefined;
};
