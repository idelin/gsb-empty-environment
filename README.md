# Android导出组件保护判定

应用商店审核时要找出导出却未受保护的 Android 组件，攻击者的应用把含 intent-filter 的 Activity、Service 与 Provider 暴露给任意应用。为Go审核组件实现可导入的清单判定包：targetSdk 低于 31 时无 android:exported 且含 intent-filter 视为导出，31 及以上缺失该属性则报错；Provider 在 targetSdk 17 以上默认不导出；组件的 android:permission 覆盖应用级设置；引用的权限未在清单定义、protectionLevel 缺失或为 normal 或 dangerous 时视为不受保护，signature 系视为受保护；activity-alias 随 targetActivity 判定。用默认值边界、权限覆盖、未定义权限和别名验证。
