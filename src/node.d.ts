/** 最小 Node.js 运行时声明（避免依赖 @types/node） */
declare const process: {
  argv: string[];
  exitCode: number | undefined;
  stdin: { read(): unknown };
};
