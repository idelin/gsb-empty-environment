declare function require(id: string): any;
declare const process: { exit(code?: number): never };
declare const console: {
  log(...data: unknown[]): void;
  error(...data: unknown[]): void;
};
