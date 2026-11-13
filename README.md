# PAM认证栈旁路推导

PAM 配置里一行 sufficient 放错位置，就可能让任何口令都通过登录。为Go配置分析包写可导入的 auth 栈判定：control 支持 required、requisite、sufficient、optional 与 [success=N default=bad]；required 失败后继续执行但终局失败，sufficient 成功且此前无 required 失败即通过；枚举模块成败组合，找出口令模块失败仍通过的路径。用 pam_permit 排在 pam_unix 前、requisite 和 success=1 验证。
