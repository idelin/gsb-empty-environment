# 实际运行结果

环境：macOS (arm64)，Python 3.11（`/opt/homebrew/bin/python3.11`），仅标准库。
时间：2026-09-25T11:20:44Z（UTC）。

## 单元测试

命令：`python3.11 -m unittest -v tests.test_evpkg`，退出码 0。

```
test_all_files_present_but_missing_reference_blocks_signoff ... ok
test_chunked_receive_and_validate_bounds_memory ... ok
test_conflicting_chunk_digest_rejected ... ok
test_correction_after_signoff_invalidates_and_requires_reverification ... ok
test_crash_after_validation_before_publish_recovers_cleanly ... ok
test_crash_mid_commit_rolls_back_signoff_and_notification ... ok
test_duplicate_delivery_confirmation_is_idempotent ... ok
test_late_chunk_from_old_manifest_rejected ... ok
test_out_of_order_receive_and_idempotent_duplicate ... ok
test_replay_is_deterministic_and_versioned ... ok
test_same_package_id_different_digest_conflicts ... ok
test_verifier_accepts_untouched_export ... ok
test_verifier_detects_missing_reference ... ok
test_verifier_detects_reordered_chunks ... ok
test_verifier_detects_replay_mismatch ... ok
test_verifier_detects_tampered_content ... ok
test_verifier_detects_tampered_manifest ... ok

Ran 17 tests in ~1.2s
OK
```

## 大文件分块内存实测

4.0 MiB 文件、64 KiB 块、共 67 块乱序接收 + 流式校验：

```
file=4.0MiB chunk=64KiB chunks=67 validate_ok=True peak_python_mem=152.4KiB
```

峰值 Python 内存 ≈152 KiB，远低于整文件 4 MiB，验证分块限内存有效
（测试断言上限 2 MiB）。

## 场景覆盖对照

- 文件全到齐但引用目标缺失 → `test_all_files_present_but_missing_reference_blocks_signoff`
- 旧清单尾块晚到被拒 → `test_late_chunk_from_old_manifest_rejected`
- 校验完成后发布前崩溃 → `test_crash_after_validation_before_publish_recovers_cleanly`
- 提交中途崩溃（签收+通知同事务回滚）→ `test_crash_mid_commit_rolls_back_signoff_and_notification`
- 签收后更正（旧签收失效、待重核查）→ `test_correction_after_signoff_invalidates_and_requires_reverification`
- 重复通知确认幂等 → `test_duplicate_delivery_confirmation_is_idempotent`
- 大文件分块限内存 → `test_chunked_receive_and_validate_bounds_memory`
- 独立验证器定位篡改内容 / 顺序 / 清单 / 引用 / 重放 → `test_verifier_detects_*`
