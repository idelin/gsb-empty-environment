// 求解器：8 位输入空间很小（每个输入 256 种取值），直接按字典序枚举。
// “最小输入”定义为：按输入声明顺序、以无符号字节（0x00..0xFF）字典序最小的元组。

import { evalSym } from "./sym.ts";
import type { Env, Sym } from "./sym.ts";

export function* enumerateEnvs(inputs: string[]): Generator<Env> {
  const n = inputs.length;
  const total = 256 ** n;
  for (let t = 0; t < total; t++) {
    let x = t;
    const env: Env = {};
    for (let i = n - 1; i >= 0; i--) {
      const byte = x % 256;
      x = Math.floor(x / 256);
      env[inputs[i]] = byte >= 128 ? byte - 256 : byte;
    }
    yield env;
  }
}

export function envKey(inputs: string[], env: Env): string {
  return inputs.map((name) => (env[name] < 0 ? env[name] + 256 : env[name])).join(",");
}

export function isSat(f: Sym, inputs: string[]): boolean {
  for (const env of enumerateEnvs(inputs)) {
    if (evalSym(f, env) !== 0) return true;
  }
  return false;
}

export function findMin(f: Sym, inputs: string[]): Env | null {
  for (const env of enumerateEnvs(inputs)) {
    if (evalSym(f, env) !== 0) return env;
  }
  return null;
}

export function collectSat(f: Sym, inputs: string[]): Set<string> {
  const out = new Set<string>();
  for (const env of enumerateEnvs(inputs)) {
    if (evalSym(f, env) !== 0) out.add(envKey(inputs, env));
  }
  return out;
}

export function fmtEnv(inputs: string[], env: Env): string {
  return inputs
    .map((name) => {
      const val = env[name];
      const byte = val < 0 ? val + 256 : val;
      return `${name}=0x${byte.toString(16).toUpperCase().padStart(2, "0")} (${val})`;
    })
    .join(", ");
}
