# CardDAV 多取与同步令牌隔离

供 `addressbook-multiget` 与 `sync-collection` REPORT 处理器调用的 TypeScript
集合隔离包。它不解析 XML，也不负责 vCard 存储；处理器把已解析的 href、令牌以及请求
URL 交给它，得到可直接映射为 207/403/404 的结构化结果。

## 用法

```ts
import {
  CardDavCollectionIsolation,
  InMemoryAddressbookStore,
  SyncTokenCodec,
  VALID_SYNC_TOKEN_ERROR_XML,
} from './index.ts';

const store = new InMemoryAddressbookStore();
const tokens = new SyncTokenCodec(process.env.SYNC_TOKEN_SECRET!);
const isolation = new CardDavCollectionIsolation({ store, tokens });

// REPORT 目标：/addressbooks/alice/default
const multiget = isolation.multiget({
  principalId: 'alice',
  collectionPath: '/addressbooks/alice/default',
  requestAuthority: 'dav.example.com',
  hrefs: parsedHrefs,
});

if (multiget.status === 404) {
  // 请求集合不存在或调用者不可读；不要泄漏两者差异
  return send404();
}

// multiget.status === 207
// items 中每一项只有 200 或统一形状的 404。
return sendMultistatus(multiget.items);

const sync = isolation.syncCollection({
  principalId: 'alice',
  collectionPath: '/addressbooks/alice/default',
  requestAuthority: 'dav.example.com',
  syncToken: parsedSyncToken, // 初次同步可省略或传空串
});

if (sync.status === 403) {
  return send(403, VALID_SYNC_TOKEN_ERROR_XML);
}
if (sync.status === 404) {
  return send404();
}

// sync.status === 207
// sync.changes 中含 200 成员与 404 墓碑，sync.syncToken 是下一轮令牌。
return sendSyncMultistatus(sync.changes, sync.syncToken);
```

`InMemoryAddressbookStore` 的 `putMember` 接受集合内成员的完整 href，`deleteMember`
删除成员并写入墓碑。每次写入/删除都会推进集合 revision，适合测试与本地开发；生产
实现只需实现 `AddressbookStore` 接口。

## 安全不变量

- **href 只解码一次。** 每个原始路径段执行一次 `decodeURIComponent`；`%252e%252e`
  只会变成字面量 `%2e%2e`，不会被二次解码成 `..`。
- **拒绝路径歧义。** 解码后出现 `/`、`\`、NUL/控制字符、空段、`.`、`..`，或原始值
  含查询串/片段时，整个 href 判为无效。编码斜杠 `%2f`、编码反斜杠 `%5c` 不会变成
  路径分隔符。
- **必须是直接成员。** 使用解码后的路径段逐段比较，要求目标恰好比集合多一段；因此
  `/default-other/x.vcf`、`/default/nested/x.vcf` 和跨集合路径都不满足。
- **集合与成员读权限。** 先校验 REPORT 目标集合可读，再校验成员 ACL。集合不可读是
  顶层 404；成员越界、不存在、已删除或不可读都返回同一个
  `{ status: 404, href }`，不会泄漏存在性。
- **同步令牌是 HMAC 认证的不透明 URI。** 签名覆盖集合 id、规范集合路径、认证主体和
  集合 revision（同步版本）。令牌互换、主体互换、路径/id 不符、签名篡改、未来
  revision、已被清理的历史 revision、格式错误，全部映射为
  `403 valid-sync-token`。
- **墓碑只对绑定主体可见。** `sync-collection` 只有在令牌属于当前集合、当前主体且
  revision 在保留区间内时，才会返回删除记录；别的集合的令牌在读变更日志之前就失败。

绝对 URI 形式的 href 必须与 `requestAuthority` 匹配；未提供 authority 时默认拒绝
绝对 URI，避免 Host 混淆。仅当明确调用 `normalizeHref` 并传入
`allowAnyAuthority: true` 时才跳过该校验。

## 验证

```sh
npm test
```

测试覆盖：百分号单次解码、编码斜杠/反斜杠、点段、集合前缀陷阱、嵌套/跨集合 href、
已删除成员、成员 ACL、无权限集合、绝对 URI 的 authority、签名篡改、未来/过期版本、
主体互换以及跨集合令牌换取删除记录。

## 生产接入注意

- `SYNC_TOKEN_SECRET` 从密钥管理读取，至少 32 字节；轮换密钥会让旧令牌统一返回
  `valid-sync-token`，客户端会执行完整重新同步。
- `AddressbookStore` 的三个读取路径（当前 revision、成员、变更日志）应来自同一个
  数据库事务快照；隔离包返回的新令牌使用该快照 revision。
- 集合 `path` 必须存为规范化后的绝对路径；数据库唯一约束应覆盖
  `(principalId, path)` 与 `(collectionId, href)`。
- 生成 207 XML 时仍须做 XML 转义；本包返回的是结构化数据，不拼接响应体。
