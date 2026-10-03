/** 离线环境无 @types/node，这里提供仓库所需的最小 Node 全局声明。 */
declare function require(id: string): any;
declare const console: {
  log(...args: any[]): void;
  error(...args: any[]): void;
};
declare const process: {
  exitCode?: number;
  env: Record<string, string | undefined>;
  argv: string[];
  cwd(): string;
};
declare function setImmediate(fn: (...args: any[]) => void): void;
