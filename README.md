# CTAP2令牌权限范围

网页或应用从平台拿到 pinUvAuthToken 后，恶意页面想用它在别的 RP 上签发断言或改动凭据管理。为Go软件认证器写供 CTAP2 请求分发调用的令牌权限组件：令牌绑定权限位（mc、ga、cm、be、lbw、acfg）与可选 rpId，mc 和 ga 必须带 rpId，cm 不得带 rpId，本题约定 mc 权限在一次成功 makeCredential 后收回，超过无操作期限或认证器重新上电即失效；校验时 rpId 与请求不符、权限位缺失都拒绝。用跨 RP 断言、cm 令牌用于 mc、过期边界和重启验证。
