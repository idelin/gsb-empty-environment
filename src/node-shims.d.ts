declare const console: {
  log(...args: unknown[]): void;
  error(...args: unknown[]): void;
};

declare const process: {
  argv: string[];
  exitCode: number | undefined;
  exit(code?: number): never;
};

declare module 'fs' {
  export function readFileSync(path: string | number, encoding: string): string;
}
