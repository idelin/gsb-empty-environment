export interface PluginManifest {
  name: string;
  version: string;
  /** 硬依赖：插件名 -> 版本范围，缺失或版本不兼容则本插件不可加载 */
  requires?: Record<string, string>;
  /** 可选依赖：存在且兼容则启用，否则静默跳过 */
  optional?: Record<string, string>;
  /** 冲突范围：插件名 -> 版本范围，命中范围的版本不得与本插件共存 */
  conflicts?: Record<string, string>;
}

export interface RootRequest {
  name: string;
  range?: string;
}
