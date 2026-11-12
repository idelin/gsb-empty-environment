import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  REFERRER_POLICIES,
  combineReferrerPolicy,
  computeReferrer,
  isDowngrade,
  isSameOrigin,
  normalizeReferrerPolicy,
  resolveRedirectChain,
  sanitizeReferrerURL,
  serializeOrigin,
  type ReferrerPolicyName,
} from './referrer-policy.ts';

const SECRET = 'token=secret-123';

function buildSourceURLs(): string[] {
  const urls: string[] = [];
  for (const scheme of ['https', 'http']) {
    for (const host of ['trust.example', 'other.example']) {
      for (const port of ['', ':8443']) {
        for (const path of ['/', '/app/home']) {
          for (const query of ['', `?${SECRET}&a=1`]) {
            for (const credential of ['', 'user:pass@']) {
              for (const fragment of ['', '#frag']) {
                urls.push(
                  `${scheme}://${credential}${host}${port}${path}${query}${fragment}`,
                );
              }
            }
          }
        }
      }
    }
  }
  return urls;
}

function buildTargetURLs(): string[] {
  const urls: string[] = [];
  for (const scheme of ['https', 'http']) {
    for (const host of ['trust.example', 'evil.example']) {
      for (const port of ['', ':8443']) {
        for (const path of ['/', '/go']) {
          urls.push(`${scheme}://${host}${port}${path}`);
        }
      }
    }
  }
  return urls;
}

/**
 * 独立于实现的策略预言机：按规范给出 (策略, 同源, 降级) 下的允许输出形态。
 * F = 完整净化地址，O = 仅 origin，N = 空串。
 */
function expectedForm(
  policy: ReferrerPolicyName,
  sameOrigin: boolean,
  downgrade: boolean,
): 'F' | 'O' | 'N' {
  switch (policy) {
    case 'no-referrer':
      return 'N';
    case 'unsafe-url':
      return 'F';
    case 'origin':
      return 'O';
    case 'strict-origin':
      return downgrade ? 'N' : 'O';
    case 'same-origin':
      return sameOrigin ? 'F' : 'N';
    case 'origin-when-cross-origin':
      return sameOrigin ? 'F' : 'O';
    case 'no-referrer-when-downgrade':
      return downgrade ? 'N' : 'F';
    case 'strict-origin-when-cross-origin':
      if (downgrade) return 'N';
      return sameOrigin ? 'F' : 'O';
  }
}

describe('sanitizeReferrerURL', () => {
  it('移除凭据与片段但保留路径和查询，且不修改入参', () => {
    const input = 'https://user:p%40ss@a.example:8443/p?q=1#f';
    const sanitized = sanitizeReferrerURL(input);
    assert.equal(sanitized.href, 'https://a.example:8443/p?q=1');
    assert.equal(input, 'https://user:p%40ss@a.example:8443/p?q=1#f');
    assert.equal(sanitized.username, '');
    assert.equal(sanitized.password, '');
    assert.equal(sanitized.hash, '');
    assert.equal(sanitized.search, '?q=1');
    assert.equal(sanitized.pathname, '/p');
  });

  it('接受 URL 对象并返回独立副本', () => {
    const input = new URL('https://a.example/p#x');
    const sanitized = sanitizeReferrerURL(input);
    assert.notEqual(sanitized, input);
    assert.equal(sanitized.href, 'https://a.example/p');
    assert.equal(input.hash, '#x');
  });
});

describe('serializeOrigin / isSameOrigin / isDowngrade', () => {
  it('origin 序列化省略默认端口并以斜杠结尾', () => {
    assert.equal(serializeOrigin('https://a.example/p?q=1'), 'https://a.example/');
    assert.equal(
      serializeOrigin('https://a.example:8443/p'),
      'https://a.example:8443/',
    );
    assert.equal(serializeOrigin('http://a.example:80/p'), 'http://a.example/');
  });

  it('同源只比较协议与主机端口，与路径查询无关', () => {
    assert.equal(
      isSameOrigin('https://a.example/x', 'https://a.example/y?z=1'),
      true,
    );
    assert.equal(isSameOrigin('https://a.example', 'https://b.example'), false);
    assert.equal(
      isSameOrigin('https://a.example:8443', 'https://a.example'),
      false,
    );
    assert.equal(isSameOrigin('https://a.example', 'http://a.example'), false);
  });

  it('降级仅取决于协议安全级，与是否跨源独立', () => {
    assert.equal(isDowngrade('https://a.example', 'http://a.example'), true);
    assert.equal(isDowngrade('https://a.example', 'http://b.example'), true);
    assert.equal(isDowngrade('http://a.example', 'https://a.example'), false);
    assert.equal(isDowngrade('https://a.example', 'https://b.example'), false);
  });
});

describe('normalizeReferrerPolicy', () => {
  it('空串回退到默认策略', () => {
    assert.equal(
      normalizeReferrerPolicy(''),
      'strict-origin-when-cross-origin',
    );
  });

  it('无法识别的令牌抛错', () => {
    assert.throws(() => normalizeReferrerPolicy('bogus'), /Invalid referrer policy/);
  });
});

describe('computeReferrer 显式期望值', () => {
  const source = 'https://user:p@trust.example/app?token=secret-123#frag';
  const sameOriginTarget = 'https://trust.example/next';
  const crossOriginTarget = 'https://other.example/next';
  // http 与 https 协议不同，因此这两个降级目标同时也是跨源目标；
  // 不存在"同源且降级"的组合（协议差异本身即构成跨源）
  const hostDowngradeTarget = 'http://trust.example/next';
  const crossDowngradeTarget = 'http://other.example/next';
  const full = 'https://trust.example/app?token=secret-123';
  const origin = 'https://trust.example/';

  const expected: Record<
    ReferrerPolicyName,
    [string, string, string, string]
  > = {
    // 列顺序：[同源, 跨源, 同主机跨协议降级, 跨主机跨协议降级]
    'no-referrer': ['', '', '', ''],
    'no-referrer-when-downgrade': [full, full, '', ''],
    'strict-origin-when-cross-origin': [full, origin, '', ''],
    'same-origin': [full, '', '', ''],
    'origin-when-cross-origin': [full, origin, origin, origin],
    'strict-origin': [origin, origin, '', ''],
    origin: [origin, origin, origin, origin],
    'unsafe-url': [full, full, full, full],
  };

  for (const policy of REFERRER_POLICIES) {
    it(policy, () => {
      const [same, cross, hostDowngrade, crossDowngrade] = expected[policy];
      assert.equal(computeReferrer(source, sameOriginTarget, policy), same);
      assert.equal(computeReferrer(source, crossOriginTarget, policy), cross);
      assert.equal(
        computeReferrer(source, hostDowngradeTarget, policy),
        hostDowngrade,
      );
      assert.equal(
        computeReferrer(source, crossDowngradeTarget, policy),
        crossDowngrade,
      );
    });
  }

  it('空串策略与默认策略结果一致', () => {
    for (const target of [
      sameOriginTarget,
      crossOriginTarget,
      hostDowngradeTarget,
    ]) {
      assert.equal(
        computeReferrer(source, target, ''),
        computeReferrer(
          source,
          target,
          'strict-origin-when-cross-origin',
        ),
      );
    }
  });
});

describe('computeReferrer URL 组合穷举', () => {
  const sources = buildSourceURLs();
  const targets = buildTargetURLs();

  it(`覆盖 ${sources.length} × ${targets.length} × ${REFERRER_POLICIES.length} 种组合`, () => {
    let checks = 0;

    for (const source of sources) {
      const sanitized = sanitizeReferrerURL(source);
      const origin = serializeOrigin(source);

      for (const target of targets) {
        const sameOrigin = isSameOrigin(source, target);
        const downgrade = isDowngrade(source, target);

        for (const policy of REFERRER_POLICIES) {
          const output = computeReferrer(source, target, policy);
          checks += 1;

          assert.ok(!output.includes('@'), `凭据泄漏: ${output}`);
          assert.ok(!output.includes('#'), `片段泄漏: ${output}`);

          const form = expectedForm(policy, sameOrigin, downgrade);
          if (form === 'N') {
            assert.equal(output, '');
          } else if (form === 'O') {
            assert.equal(output, origin);
            assert.ok(!output.includes(SECRET), `秘密查询泄漏: ${output}`);
          } else {
            assert.equal(output, sanitized.href);
          }

          if (output.includes(SECRET)) {
            assert.equal(output, sanitized.href);
            assert.equal(form, 'F');
            if (
              policy === 'no-referrer-when-downgrade' ||
              policy === 'strict-origin-when-cross-origin' ||
              policy === 'strict-origin'
            ) {
              assert.equal(downgrade, false);
            }
            if (
              policy === 'same-origin' ||
              policy === 'strict-origin-when-cross-origin' ||
              policy === 'origin-when-cross-origin'
            ) {
              assert.equal(sameOrigin, true);
            }
          }
        }
      }
    }

    assert.equal(
      checks,
      sources.length * targets.length * REFERRER_POLICIES.length,
    );
  });
});

describe('combineReferrerPolicy', () => {
  it('逐跳只收紧不放宽', () => {
    assert.equal(combineReferrerPolicy('origin', ['unsafe-url']), 'origin');
    assert.equal(combineReferrerPolicy('origin', ['no-referrer']), 'no-referrer');
    assert.equal(
      combineReferrerPolicy('strict-origin', [
        'unsafe-url',
        'same-origin',
        'origin',
      ]),
      'same-origin',
    );
  });

  it('忽略无效令牌，无有效令牌时维持当前策略', () => {
    assert.equal(
      combineReferrerPolicy('origin', ['bogus', '']),
      'origin',
    );
  });

  it('空串当前策略先归一化再参与组合', () => {
    assert.equal(combineReferrerPolicy('', ['no-referrer']), 'no-referrer');
    assert.equal(
      combineReferrerPolicy('', ['unsafe-url']),
      'strict-origin-when-cross-origin',
    );
  });

  it('严格度序与八种策略覆盖一致', () => {
    let policy: ReferrerPolicyName = 'unsafe-url';
    const chain: ReferrerPolicyName[] = [
      'origin',
      'strict-origin',
      'origin-when-cross-origin',
      'same-origin',
      'strict-origin-when-cross-origin',
      'no-referrer-when-downgrade',
      'no-referrer',
    ];
    for (const next of chain) {
      policy = combineReferrerPolicy(policy, [next]);
      assert.equal(policy, next);
    }
    for (const candidate of REFERRER_POLICIES) {
      assert.equal(combineReferrerPolicy('no-referrer', [candidate]), 'no-referrer');
    }
  });
});

describe('resolveRedirectChain', () => {
  const source = 'https://user:p@trust.example/start?token=secret-123#f';

  it('同源重定向更新内部引用地址，跨源重定向保留原引用', () => {
    const results = resolveRedirectChain(
      source,
      'https://trust.example/a',
      'unsafe-url',
      [
        { location: 'https://trust.example/b?next=1' },
        { location: 'https://other.example/c' },
      ],
    );

    assert.equal(results.length, 3);
    assert.equal(
      results[0].internalReferrer,
      'https://trust.example/start?token=secret-123',
    );
    assert.equal(
      results[0].referrer,
      'https://trust.example/start?token=secret-123',
    );
    assert.equal(
      results[1].internalReferrer,
      'https://trust.example/b?next=1',
    );
    assert.equal(results[1].referrer, 'https://trust.example/b?next=1');
    assert.equal(
      results[2].internalReferrer,
      'https://trust.example/b?next=1',
    );
    assert.equal(results[2].referrer, 'https://trust.example/b?next=1');
  });

  it('响应策略逐跳收紧，宽松策略不会放宽', () => {
    const results = resolveRedirectChain(
      source,
      'https://trust.example/a',
      'origin',
      [
        {
          location: 'https://trust.example/b',
          referrerPolicy: ['same-origin'],
        },
        {
          location: 'https://other.example/c',
          referrerPolicy: ['unsafe-url'],
        },
        {
          location: 'http://other.example/d',
          referrerPolicy: ['no-referrer'],
        },
      ],
    );

    assert.equal(results[0].policy, 'origin');
    assert.equal(results[0].referrer, 'https://trust.example/');
    assert.equal(results[1].policy, 'same-origin');
    assert.equal(results[1].referrer, 'https://trust.example/b');
    assert.equal(results[2].policy, 'same-origin');
    assert.equal(results[2].referrer, '');
    assert.equal(results[3].policy, 'no-referrer');
    assert.equal(results[3].referrer, '');
  });

  it('降级重定向后严格策略仅发送 origin 或空串', () => {
    const results = resolveRedirectChain(
      source,
      'https://trust.example/a',
      'strict-origin-when-cross-origin',
      [
        {
          location: 'https://trust.example/b',
          referrerPolicy: ['unsafe-url'],
        },
        { location: 'http://evil.example/c' },
      ],
    );

    assert.equal(
      results[0].referrer,
      'https://trust.example/start?token=secret-123',
    );
    assert.equal(results[1].policy, 'strict-origin-when-cross-origin');
    assert.equal(results[1].referrer, 'https://trust.example/b');
    assert.equal(results[2].referrer, '');

    for (const result of results) {
      assert.ok(!result.referrer.includes('@'));
      assert.ok(!result.referrer.includes('#'));
      assert.ok(!result.internalReferrer.includes('@'));
      assert.ok(!result.internalReferrer.includes('#'));
      if (new URL(result.requestURL).protocol === 'http:') {
        assert.equal(result.referrer, '');
      }
      if (result.referrer.includes(SECRET)) {
        assert.equal(new URL(result.requestURL).protocol, 'https:');
        assert.equal(
          isSameOrigin(result.internalReferrer, result.requestURL),
          true,
        );
      }
    }
  });

  it('无重定向时只返回首跳结果', () => {
    const results = resolveRedirectChain(
      source,
      'https://other.example/a',
      'origin',
      [],
    );
    assert.equal(results.length, 1);
    assert.equal(results[0].hop, 0);
    assert.equal(results[0].referrer, 'https://trust.example/');
  });

  it('跨源重定向的响应策略仍可继续收紧', () => {
    const results = resolveRedirectChain(
      source,
      'https://trust.example/a',
      'unsafe-url',
      [
        {
          location: 'https://other.example/b',
          referrerPolicy: ['origin'],
        },
        {
          location: 'http://other.example/c',
          referrerPolicy: ['no-referrer'],
        },
      ],
    );

    assert.equal(results[1].policy, 'origin');
    assert.equal(results[1].referrer, 'https://trust.example/');
    assert.equal(results[2].policy, 'no-referrer');
    assert.equal(results[2].referrer, '');
  });
});
