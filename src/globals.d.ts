// 本环境没有 @types/node，最小声明 verify.ts 用到的 Node 全局。
declare const process: {
  exitCode: number | undefined;
};

declare function setImmediate(callback: () => void): unknown;
