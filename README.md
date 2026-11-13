# 令牌兑换源相等

私人状态令牌在兄弟源上兑换。兑换源必须与发行时绑定的源全等，不接受父域或子域。同一源的首次兑换成功。由发行适配调用可导入的TypeScript库。改用父域和子域各兑换一次。

## 实现

- `PrivateStateTokenService` 发行令牌时把规范化后的完整源写入令牌，并用 HMAC-SHA256 对源、随机 nonce、发行时间和过期时间签名。
- 兑换时先校验签名和过期时间，再做完整的源元组比较（scheme、host、port）；不使用字符串前后缀、父域或 eTLD+1 匹配。
- 兑换失败不会消耗令牌；在绑定源上的首次兑换成功，同一 nonce 的再次兑换返回 `already-redeemed`。
- `IssuanceAdapter` 只负责调用可导入的库，不在适配层复制源绑定逻辑。

```ts
import {
  createIssuanceAdapter,
  PrivateStateTokenService,
} from "./index.ts";

const service = new PrivateStateTokenService({
  issuer: "https://issuer.example",
  secret: process.env.TOKEN_SIGNING_SECRET!,
});
const adapter = createIssuanceAdapter(service);

const token = adapter.issueToken("https://shop.example.com");

service.redeem(token, "https://shop.example.com"); // ok: true
service.redeem(token, "https://example.com"); // ok: false, origin-mismatch
service.redeem(token, "https://checkout.shop.example.com"); // origin-mismatch
```

## 测试

`npm test` 覆盖同源首次兑换、父域和子域各兑换一次、兄弟源、不同 scheme/port、令牌篡改、重放和过期场景。

重放记录保存在进程内，适用于测试和单实例部署；多实例部署需要把 nonce 占用检查放入共享的原子存储。
