# Play Integrity判定请求绑定

高风险操作用 Play Integrity 令牌解码后的判定做授权，攻击者把为无关请求拿到的令牌转发给敏感接口，或重放旧令牌。为Go业务服务实现可导入的判定绑定包：requestDetails.requestHash 必须等于该请求体 SHA-256 的 URL-safe base64（无填充），requestPackageName 与 appIntegrity.packageName 都等于本应用，timestampMillis 与服务端时间相差超过 60 秒拒绝，同一 requestHash 与时间戳的组合只接受一次；appRecognitionVerdict 须为 PLAY_RECOGNIZED，certificateSha256Digest 须包含已登记摘要，deviceRecognitionVerdict 须含 MEETS_DEVICE_INTEGRITY。用请求转发、重放、边界时间与错摘要验证。
