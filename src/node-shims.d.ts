// Minimal Node.js declarations (no @types/node available offline).
declare module "fs" {
  export function readFileSync(path: string, encoding: string): string;
  export function existsSync(path: string): boolean;
}
declare module "path" {
  export function resolve(...parts: string[]): string;
  export function dirname(p: string): string;
}
declare const process: {
  argv: string[];
  exitCode?: number;
  cwd(): string;
  stdout: { write(s: string): void };
  stderr: { write(s: string): void };
};
