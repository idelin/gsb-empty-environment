package netsec_test

import (
	"fmt"
	"time"

	netsec "example.org/netsec/q0465"
)

func ExampleAttributor() {
	svc := netsec.NewAttributor(
		netsec.WithRebindDelay(24*time.Hour),
		netsec.WithMaxAmount(5_000_000),
	)

	_ = svc.RegisterAcquirer(netsec.Acquirer{ID: "acq-1"})
	_ = svc.RegisterMerchant(netsec.Merchant{ID: "merchant-1"})
	_ = svc.RegisterSettlementAccount(netsec.SettlementAccount{
		ID: "account-1", MerchantID: "merchant-1", AcquirerID: "acq-1",
	})
	_ = svc.SignTerminal(netsec.TerminalBinding{
		TerminalID: "terminal-1", MerchantID: "merchant-1",
		AcquirerID: "acq-1", AccountID: "account-1",
	})

	settlement, err := svc.Attribute(netsec.SettlementRequest{
		Reference: "batch-42-row-7", TerminalID: "terminal-1",
		MerchantID: "merchant-1", AcquirerID: "acq-1", AmountCents: 12_500,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(settlement.AccountID)
	// Output: account-1
}
