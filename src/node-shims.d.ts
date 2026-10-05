declare module 'fs' {
  export function readFileSync(path: string, encoding: string): string;
}

declare const process: {
  argv: string[];
  exit(code?: number): never;
};

declare const console: {
  log(...args: unknown[]): void;
  error(...args: unknown[]): void;
};
