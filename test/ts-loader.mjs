/**
 * 仅用于零依赖开发环境下直接运行 TypeScript 测试：
 * 把相对的 .js 说明符解析到同目录 .ts 源文件，再交给 Node 的
 * --experimental-strip-types 处理。发布构建仍走 tsc，产物使用真实 .js。
 */
export async function resolve(specifier, context, nextResolve) {
  if (specifier.startsWith(".") && specifier.endsWith(".js")) {
    try {
      return await nextResolve(specifier.slice(0, -3) + ".ts", context);
    } catch {
      // 回退到默认解析（例如实际存在的 .js 文件）。
    }
  }
  return nextResolve(specifier, context);
}
