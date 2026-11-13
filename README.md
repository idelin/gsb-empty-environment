# Android网络安全配置的域继承与固定过期

应用对自家域强制证书固定，又给调试构建放宽，配置层层嵌套后容易让明文或用户证书悄悄放行。为Go构建期检查包写可导入的 network-security-config 求值：最具体的 domain-config 生效并继承父项信任锚与固定集合；cleartextTrafficPermitted 缺省在 targetSdk 28 及以上为 false；固定集合过期后不再强制；debug-overrides 仅调试构建生效；用户 CA 仅在 targetSdk 低于 24 缺省信任。用嵌套继承、过期和调试构建验证。
