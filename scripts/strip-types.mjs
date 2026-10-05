// 离线环境下的最小类型擦除器，仅用于在没有 tsc 时运行测试。
// 它只支持 src/ 中刻意遵守的 TypeScript 子集：
//   - 类型标注一律写成 `name : Type`（冒号前有空格），对象字面量键后不留空格；
//   - 不使用泛型标注（除 Set<T>/Map<K,V> 这类无逗号形式）、枚举、类、as 断言、三元表达式；
//   - 类型只通过 `import type` / `export interface` / `export type` 声明；
//   - 字符串与注释中不出现 ` : `（空格-冒号-空格）。
// 有网络时请使用 `npm run typecheck`（tsc）做真正的类型检查。

const ANNOTATION = / : [A-Za-z0-9_[\]|.'"&<>]+(?: [A-Za-z0-9_[\]|.'"&<>]+)* *(?=[,)=;{]|$)/gm;
const IMPORT_TYPE = /^\s*import\s+type\s/;
const TYPE_ALIAS = /^\s*(export\s+)?type\s/;
const INTERFACE = /^\s*(export\s+)?interface\s/;
const GENERIC_CALL = /(new [A-Za-z]+)<[^<>]*>/g;

function countChar(line, ch) {
  let count = 0;
  for (const c of line) if (c === ch) count += 1;
  return count;
}

export function stripTypes(source) {
  const out = [];
  let interfaceDepth = 0;
  let skippingType = false;
  for (const line of source.split('\n')) {
    if (interfaceDepth > 0) {
      interfaceDepth += countChar(line, '{') - countChar(line, '}');
      continue;
    }
    if (skippingType) {
      if (line.includes(';')) skippingType = false;
      continue;
    }
    if (IMPORT_TYPE.test(line)) {
      if (!line.includes(';')) skippingType = true;
      continue;
    }
    if (TYPE_ALIAS.test(line)) {
      if (!line.includes(';')) skippingType = true;
      continue;
    }
    if (INTERFACE.test(line)) {
      interfaceDepth = countChar(line, '{') - countChar(line, '}');
      continue;
    }
    out.push(line.replace(ANNOTATION, '').replace(GENERIC_CALL, '$1'));
  }
  return out.join('\n');
}
