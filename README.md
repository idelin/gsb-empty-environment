# Composer锁content-hash与dist引用

composer.lock 的 content-hash 用来发现 composer.json 被改而锁没更新，dist 引用也可能被改成指向别处，攻击者借此绕过依赖审查。为Go构建网关写可导入的 Composer 锁校验包：按本题约定，content-hash 为 MD5(对 composer.json 的 name、version、require、require-dev、conflict、replace、provide、minimum-stability、prefer-stable、repositories 字段子集按键名排序后的紧凑 JSON)，与锁中值不符拒绝；packages 中每个条目的 dist.type 须为 zip，dist.url 主机须在调用方白名单内，dist.reference 须等于 source.reference，dist.shasum 非空时与实际包的 SHA-1 一致；repositories 里出现 type 为 path 或 vcs 且主机不在白名单的条目拒绝。用修改 require 而不更新锁、reference 不符、url 主机不在白名单和 shasum 不符验证。
