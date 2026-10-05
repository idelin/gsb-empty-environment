declare module "fs" {
  export function readFileSync(path: string, encoding: string): string;
  export function existsSync(path: string): boolean;
}

declare const process: {
  argv: string[];
  exit(code?: number): never;
};
