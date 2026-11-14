# netsec — 防重放会话认证组件

供 C++20 设备管理代理调用的可导入会话认证库，解决 TACACS+ 类认证报文被
抓取重放后设备误认旧授权结果的问题。

## 设计

- 响应 = HMAC-SHA-256(key, 域分隔符 ‖ 客户端地址 ‖ 会话ID ‖ 密钥代次 ‖ 序列号 ‖ 挑战随机数)
- 会话绑定客户端地址、随机挑战、单调递增序列号与密钥代次
- 挑战一次性使用：验证通过后必须调用 `nextChallenge` 换取新挑战
- 时间窗（`challenge_ttl`）与失败重传上限（`max_attempts`，超限锁定会话）均有界
- `rotateKey` / 重启（状态全在内存）/ `notifyConfigRollback` 使旧会话全部失效
- 摘要比对使用常数时间比较

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：合法登录、报文重放、挑战替换、序列回退、密钥轮换、重启失效、
配置回滚、时间窗过期、重传上限、客户端地址绑定，以及 RFC 4231 HMAC 向量。

## 用法

```cpp
netsec::SessionAuthenticator auth(key);            // 设备侧
auto ch = auth.beginSession(client_addr);          // 下发挑战
auto r = auth.verifyResponse(id, client_addr, ch.key_generation,
                             ch.sequence, ch.nonce, response);
if (r == netsec::VerifyResult::kOk)
  ch = *auth.nextChallenge(id);                    // 换发下一次挑战
```
