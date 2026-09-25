/** 可插拔摘要哈希函数：输入规范编码字符串，输出摘要字符串。 */
export type HashFn = (input: string) => string;

/** 默认 FNV-1a 32 位哈希（十六进制）。测试可注入碰撞哈希做碰撞复核。 */
export const fnv1a: HashFn = (input: string): string => {
  let h = 0x811c9dc5;
  for (let i = 0; i < input.length; i++) {
    h ^= input.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h.toString(16).padStart(8, '0');
};

export const defaultHash: HashFn = fnv1a;
