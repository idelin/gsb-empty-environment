# 示例文档

先定义共享变量：

```run name=setup
set user world
set lang Go
print setup done
```

后面的块通过 `needs` 声明依赖，并读取共享命名空间：

```run name=greet needs=setup
print hello $user from $lang
```

失败的块只会跳过依赖它的块：

```run name=fragile
set temp 1
fail network unreachable
```

```run name=after-fragile needs=fragile
print never printed
```

独立块不受影响，且 `temp` 不会泄漏：

```run name=independent
print still running
```
