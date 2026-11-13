import { bytesToHex, sha256, utf8 } from "./bytes.ts";
import { Chain, runtimeFor } from "./chain.ts";
import { VulnerableHost } from "./host.ts";
import { deriveAddress } from "./record.ts";

export type ExploitOp =
  | "deploy-honest"
  | "deploy-evil"
  | "destroy"
  | "call";

export interface ExploitFixture {
  deployer: Uint8Array;
  salt: Uint8Array;
  initCode: Uint8Array;
  honestArgs: Uint8Array;
  evilArgs: Uint8Array;
}

export function defaultFixture(): ExploitFixture {
  return {
    deployer: utf8("deployer-00000000001"),
    salt: sha256(utf8("salt")),
    initCode: utf8("init-code-v1"),
    honestArgs: utf8("constructor-args-honest"),
    evilArgs: utf8("constructor-args-evil"),
  };
}

interface World {
  chain: Chain;
  host: VulnerableHost;
  address: Uint8Array;
  honestRuntimeDigestHex: string;
  pwned: boolean;
}

function freshWorld(fixture: ExploitFixture): World {
  const chain = new Chain(1);
  const host = new VulnerableHost(chain);
  const address = deriveAddress(
    fixture.deployer,
    fixture.salt,
    sha256(fixture.initCode),
  );
  const honestRuntime = runtimeFor(fixture.initCode, fixture.honestArgs);
  const honestRuntimeDigestHex = bytesToHex(sha256(honestRuntime));
  // 授权系统提前批准该确定性地址（待审实现只按地址缓存）。
  host.preAuthorize(address, sha256(honestRuntime));
  return { chain, host, address, honestRuntimeDigestHex, pwned: false };
}

function apply(world: World, op: ExploitOp, fixture: ExploitFixture): boolean {
  const { chain, host, address } = world;
  try {
    switch (op) {
      case "deploy-honest":
        host.onDeployed(
          chain.deploy({
            deployer: fixture.deployer,
            salt: fixture.salt,
            initCode: fixture.initCode,
            constructorArgs: fixture.honestArgs,
          }),
        );
        return true;
      case "deploy-evil":
        host.onDeployed(
          chain.deploy({
            deployer: fixture.deployer,
            salt: fixture.salt,
            initCode: fixture.initCode,
            constructorArgs: fixture.evilArgs,
          }),
        );
        return true;
      case "destroy":
        chain.destroy(address);
        return true;
      case "call": {
        host.call(address);
        // 敏感回调已执行：若当前代码摘要与激活时不同，则攻击成功。
        const current = chain.codeDigestOf(address);
        if (current && bytesToHex(current) !== world.honestRuntimeDigestHex) {
          world.pwned = true;
        }
        return true;
      }
    }
  } catch {
    return false;
  }
}

/**
 * 在待审实现上广度优先搜索最短的 Deploy/Destroy/Deploy 攻击序列：
 * 找到使“不同 runtimeCode 成功执行敏感回调”的最短操作序列。
 */
export function findShortestExploit(
  fixture: ExploitFixture = defaultFixture(),
  maxDepth = 6,
): ExploitOp[] | null {
  const ops: ExploitOp[] = ["deploy-honest", "deploy-evil", "destroy", "call"];
  let frontier: { world: World; trace: ExploitOp[] }[] = [
    { world: freshWorld(fixture), trace: [] },
  ];
  for (let depth = 1; depth <= maxDepth; depth += 1) {
    const next: { world: World; trace: ExploitOp[] }[] = [];
    for (const node of frontier) {
      for (const op of ops) {
        const world = freshWorld(fixture);
        let ok = true;
        for (const step of [...node.trace, op]) {
          if (!apply(world, step, fixture)) {
            ok = false;
            break;
          }
        }
        if (!ok) continue;
        const trace = [...node.trace, op];
        if (world.pwned) return trace;
        next.push({ world, trace });
      }
    }
    frontier = next;
  }
  return null;
}
