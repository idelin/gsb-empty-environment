// 最小 Node 环境声明（环境离线，无法安装 @types/node）
declare const process: {
  argv: string[];
  exit(code?: number): never;
};
