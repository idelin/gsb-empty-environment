// Package netsec implements an importable flash-loan constraint component for
// Go lending pools.
//
// A Guard tracks every borrow, repayment, collateral change, price update and
// liquidation by (account, block). It rejects same-block compositions such as
// flash-borrow, price manipulation, liquidation and repayment, enforces price
// and liquidation cooldowns, and marks abnormal profit and duplicate
// liquidations. RunAtomic, AtBlock and RunFlashLoan provide all-or-nothing
// state rollback, while risk detections remain in an append-only telemetry
// ledger.
package netsec
