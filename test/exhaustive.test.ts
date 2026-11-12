import { test } from "node:test";
import assert from "node:assert/strict";
import {
  HostAdmission,
  buildCacheKey,
  createDefaultMatrix,
  enumerateKeys,
  parseOrigin,
  serializeKey,
  type AdmissionInput,
  type PolicyKey,
} from "../index.ts";

/**
 * 为每一种来源关系提供真实可解析的资源 URL（同一固定资源路径），
 * 从而保证“同一资源”仅在安全上下文上有差异。
 */
const RESOURCE_ORIGINS = {
  "same-origin": "https://app.example.com/resource",
  "same-site": "https://cdn.example.com/resource",
  "cross-site": "https://resource.third-party.org/resource",
} as const;

const DOC = parseOrigin("https://app.example.com")!;

function inputFromKey(key: PolicyKey): AdmissionInput {
  return {
    documentOrigin: DOC,
    resourceUrl: RESOURCE_ORIGINS[key.relationship],
    resourceMode: key.resourceMode,
    credentials: key.credentials,
    embedding: key.embedding,
    declaration: { corp: key.corp, corsAccess: key.corsAccess },
  };
}

test("穷举：648 个有限组合的判定与矩阵一致，且结果确定", () => {
  const matrix = createDefaultMatrix();
  const host = new HostAdmission(matrix);
  const keys = enumerateKeys();
  assert.equal(keys.length, 648);

  for (const key of keys) {
    const first = host.admit(inputFromKey(key));
    const second = host.admit(inputFromKey(key));
    const expected = matrix[serializeKey(key)];
    assert.equal(first.allowed, expected === "allow");
    assert.equal(second.allowed, first.allowed);
    assert.equal(second.fromCache, first.allowed);
  }
});

test("穷举证明：同一资源在任意两个不同安全上下文之间缓存键互不相同", () => {
  const matrix = createDefaultMatrix();
  const host = new HostAdmission(matrix);
  const keys = enumerateKeys();

  // 先让所有 allow 组合写入缓存。
  const allowKeys: PolicyKey[] = [];
  for (const key of keys) {
    const result = host.admit(inputFromKey(key));
    if (result.allowed) {
      allowKeys.push(key);
    }
  }

  // 缓存条目数等于获许组合数：没有任何两个上下文共享条目。
  assert.equal(host.cacheSize, allowKeys.length);

  // 对同一资源路径的全部 648 个上下文两两比较缓存键。
  const seenKeys = new Set<string>();
  for (const key of keys) {
    const input = inputFromKey(key);
    const cacheKey = buildCacheKey(input.resourceUrl, DOC, key);
    assert.ok(!seenKeys.has(cacheKey), `缓存键冲突: ${cacheKey}`);
    seenKeys.add(cacheKey);
  }
  assert.equal(seenKeys.size, keys.length);

  // 逐位证明：固定其他维度，仅翻转一个维度，缓存键必然改变。
  const fields = [
    "relationship",
    "resourceMode",
    "credentials",
    "embedding",
    "corp",
    "corsAccess",
  ] as const;
  for (const base of keys) {
    for (const field of fields) {
      for (const variant of keys) {
        const othersDiffer = fields.some(
          (f) => f !== field && base[f] !== variant[f],
        );
        if (othersDiffer || base[field] === variant[field]) {
          continue;
        }
        const a = buildCacheKey(
          RESOURCE_ORIGINS[base.relationship],
          DOC,
          base,
        );
        const b = buildCacheKey(
          RESOURCE_ORIGINS[variant.relationship],
          DOC,
          variant,
        );
        assert.notEqual(a, b, `维度 ${field} 未参与缓存隔离`);
      }
    }
  }
});

test("穷举证明：被拒上下文永远无法读到另一上下文为同一资源缓存的响应", () => {
  const matrix = createDefaultMatrix();
  const host = new HostAdmission(matrix);
  const keys = enumerateKeys();

  // 所有宽松（allow）上下文先把资源写入缓存。
  for (const key of keys) {
    if (matrix[serializeKey(key)] === "allow") {
      host.admit(inputFromKey(key));
    }
  }

  // 所有严格（deny）上下文查找缓存必须全部落空。
  for (const key of keys) {
    if (matrix[serializeKey(key)] === "deny") {
      const input = inputFromKey(key);
      assert.equal(
        host.lookupCache(input),
        undefined,
        `隔离被缓存绕过: ${serializeKey(key)}`,
      );
    }
  }

  // 即使资源 URL 完全相同，跨关系也不命中：
  // 跨站 require-corp + 无 CORP 被拒，而同站 same-site CORP 已获许缓存。
  const allowedSameSite = host.lookupCache({
    documentOrigin: DOC,
    resourceUrl: "https://cdn.example.com/resource",
    resourceMode: "no-cors",
    credentials: "omit",
    embedding: "require-corp",
    declaration: { corp: "same-site", corsAccess: false },
  });
  assert.ok(allowedSameSite);
  const deniedCrossSite = host.lookupCache({
    documentOrigin: DOC,
    resourceUrl: "https://resource.third-party.org/resource",
    resourceMode: "no-cors",
    credentials: "omit",
    embedding: "require-corp",
    declaration: { corp: "none", corsAccess: false },
  });
  assert.equal(deniedCrossSite, undefined);
});

test("拒绝不写入缓存，且同一上下文重复访问才命中缓存", () => {
  const host = new HostAdmission(createDefaultMatrix());
  const denied: AdmissionInput = {
    documentOrigin: DOC,
    resourceUrl: "https://resource.third-party.org/resource",
    resourceMode: "no-cors",
    credentials: "omit",
    embedding: "require-corp",
    declaration: { corp: "none", corsAccess: false },
  };
  const before = host.cacheSize;
  assert.equal(host.admit(denied).allowed, false);
  assert.equal(host.cacheSize, before);

  const allowed: AdmissionInput = {
    ...denied,
    declaration: { corp: "cross-origin", corsAccess: false },
  };
  const first = host.admit(allowed);
  assert.equal(first.allowed, true);
  assert.equal(first.fromCache, false);
  const second = host.admit(allowed);
  assert.equal(second.fromCache, true);
  assert.equal(second.cacheKey, first.cacheKey);
});

test("重定向后缓存只绑定末跳的完整安全上下文", () => {
  const host = new HostAdmission(createDefaultMatrix());
  const result = host.admitRedirectChain({
    documentOrigin: DOC,
    resourceMode: "no-cors",
    credentials: "omit",
    embedding: "require-corp",
    hops: [
      {
        resourceUrl: "https://cdn.example.com/redirect",
        declaration: { corp: "same-site", corsAccess: false },
      },
      {
        resourceUrl: "https://resource.third-party.org/final",
        declaration: { corp: "cross-origin", corsAccess: false },
      },
    ],
  });
  assert.equal(result.allowed, true);

  // 末跳上下文可命中缓存。
  assert.ok(
    host.lookupCache({
      documentOrigin: DOC,
      resourceUrl: "https://resource.third-party.org/final",
      resourceMode: "no-cors",
      credentials: "omit",
      embedding: "require-corp",
      declaration: { corp: "cross-origin", corsAccess: false },
    }),
  );

  // 同样是末跳 URL，换成被矩阵拒绝的声明则读不到缓存。
  assert.equal(
    host.lookupCache({
      documentOrigin: DOC,
      resourceUrl: "https://resource.third-party.org/final",
      resourceMode: "no-cors",
      credentials: "omit",
      embedding: "require-corp",
      declaration: { corp: "none", corsAccess: false },
    }),
    undefined,
  );

  // 重定向链中途被拒时不产生任何缓存写入。
  const sizeBefore = host.cacheSize;
  const blocked = host.admitRedirectChain({
    documentOrigin: DOC,
    resourceMode: "no-cors",
    credentials: "omit",
    embedding: "require-corp",
    hops: [
      {
        resourceUrl: "https://cdn.example.com/redirect2",
        declaration: { corp: "same-site", corsAccess: false },
      },
      {
        resourceUrl: "https://evil.example.org/final",
        declaration: { corp: "none", corsAccess: false },
      },
    ],
  });
  assert.equal(blocked.allowed, false);
  assert.equal(host.cacheSize, sizeBefore);
});

test("随机化访问顺序不改变隔离结论", () => {
  const matrix = createDefaultMatrix();
  const keys = enumerateKeys();
  for (let round = 0; round < 3; round += 1) {
    const host = new HostAdmission(matrix);
    const shuffled = [...keys];
    for (let i = shuffled.length - 1; i > 0; i -= 1) {
      const j = Math.floor(Math.random() * (i + 1));
      [shuffled[i], shuffled[j]] = [shuffled[j], shuffled[i]];
    }
    let allowCount = 0;
    for (const key of shuffled) {
      if (host.admit(inputFromKey(key)).allowed) {
        allowCount += 1;
      }
    }
    assert.equal(host.cacheSize, allowCount);
    for (const key of keys) {
      if (matrix[serializeKey(key)] === "deny") {
        assert.equal(host.lookupCache(inputFromKey(key)), undefined);
      }
    }
  }
});
