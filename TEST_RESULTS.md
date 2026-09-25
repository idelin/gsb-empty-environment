$ python3.11 -m unittest -v tests.test_masked_product
test_batch_is_atomic_on_user_exception_and_cancel (tests.test_masked_product.MaskedProductTests.test_batch_is_atomic_on_user_exception_and_cancel) ... ok
test_cancel_and_withdraw_restores_cancelled_pair (tests.test_masked_product.MaskedProductTests.test_cancel_and_withdraw_restores_cancelled_pair) ... ok
test_cancelled_batch_leaves_no_private_spill (tests.test_masked_product.MaskedProductTests.test_cancelled_batch_leaves_no_private_spill) ... ok
test_concurrent_reader_keeps_pinned_old_result (tests.test_masked_product.MaskedProductTests.test_concurrent_reader_keeps_pinned_old_result) ... ok
test_everything_spilled_threshold_one (tests.test_masked_product.MaskedProductTests.test_everything_spilled_threshold_one) ... ok
test_hot_rows_spill_and_remain_bounded (tests.test_masked_product.MaskedProductTests.test_hot_rows_spill_and_remain_bounded) ... ok
test_mask_delete_is_only_visibility_and_rejoin_restores (tests.test_masked_product.MaskedProductTests.test_mask_delete_is_only_visibility_and_rejoin_restores) ... ok
test_noop_transaction_reuses_published_version (tests.test_masked_product.MaskedProductTests.test_noop_transaction_reuses_published_version) ... ok
test_old_snapshot_remains_after_new_commit_sorted (tests.test_masked_product.MaskedProductTests.test_old_snapshot_remains_after_new_commit_sorted) ... ok
test_random_mixed_transactions_match_dense_and_no_orphans (tests.test_masked_product.MaskedProductTests.test_random_mixed_transactions_match_dense_and_no_orphans) ... ok
test_repeated_updates_do_not_accumulate_orphans (tests.test_masked_product.MaskedProductTests.test_repeated_updates_do_not_accumulate_orphans) ... ok
test_spill_failure_leaves_published_result (tests.test_masked_product.MaskedProductTests.test_spill_failure_leaves_published_result) ... ok

----------------------------------------------------------------------
Ran 12 tests in 0.302s

OK

$ python3.11 demo_replay.py
-- two k-paths cancel at (0,0); (1,0)=12
version=(2, 2, 2) result=[(0, 0, 0), (0, 1, 2), (1, 0, 12), (1, 1, 4)]
memory=2 spilled_rows=1 active=5
-- withdraw one path: (0,0) is restored to 6
version=(3, 2, 2) result=[(0, 0, 6), (0, 1, 2), (1, 0, 12), (1, 1, 4)]
memory=2 spilled_rows=1 active=4
sources(0,0)=1
pinned old result=[(0, 0, 0), (0, 1, 2), (1, 0, 12), (1, 1, 4)]
-- mask removal changes visibility only: (0,1) hidden
version=(3, 2, 3) result=[(0, 0, 6), (1, 0, 12)]
memory=1 spilled_rows=1 active=2
-- mask rejoin restores (0,1)=2
version=(3, 2, 4) result=[(0, 0, 6), (0, 1, 2), (1, 0, 12), (1, 1, 4)]
memory=2 spilled_rows=1 active=4
-- B update inside one transaction
version=(3, 3, 4) result=[(0, 0, 6), (0, 1, 10), (1, 0, 12), (1, 1, 20)]
memory=2 spilled_rows=1 active=4
