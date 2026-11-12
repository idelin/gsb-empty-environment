# Private Aggregation协调方来源与上下文校验

广告脚本在隔离的 worklet 里提交聚合贡献，恶意脚本可以指定任意聚合协调方，让加密报告流向自己控制的服务，或借畸形值与伪造的上下文标识干扰聚合结果。为TypeScript浏览器宿主写可导入的贡献准入包，供 contributeToHistogram 调用：aggregation_coordinator_origin 必须在宿主登记的白名单内，否则整个调用抛 TypeError；bucket 须为 [0, 2^128) 内的整数，value 须为非负整数，负值或浮点拒绝；同一 context_id 的贡献共享一份报告，最多 20 条，超出的丢弃；context_id 长度不超过 64 字节；filtering_id 须为 0 到 255 的整数，且同一份报告内只能使用同一个值；调试模式只有在第三方 Cookie 可用时才生效，否则按未开启处理；贡献内容在 worklet 之外不可观察。用未登记协调方、负值、第 21 条贡献、context_id 过长、filtering_id 冲突和 Cookie 不可用时的调试模式验证。
