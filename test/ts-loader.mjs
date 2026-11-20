/**
 * 仅供测试使用的极简 ESM 解析钩子：
 * 将相对导入中的 `.js` 说明符解析回同目录的 `.ts` 源文件，
 * 使 NodeNext 风格（扩展名写 .js）的 TypeScript 源码可直接被
 * Node 的类型擦除执行器加载，无需编译步骤。
 */
import { existsSync } from 'node:fs';
import { dirname, resolve as resolvePath } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

export async function resolve(specifier, context, nextResolve) {
  if (
    (specifier.startsWith('./') || specifier.startsWith('../')) &&
    specifier.endsWith('.js')
  ) {
    const parentPath = fileURLToPath(context.parentURL);
    const candidatePath = resolvePath(
      dirname(parentPath),
      specifier.replace(/\.js$/, '.ts'),
    );
    const candidateUrl = pathToFileURL(candidatePath).href;
    if (existsSync(candidatePath)) {
      return { url: candidateUrl, shortCircuit: true };
    }
  }
  return nextResolve(specifier, context);
}
