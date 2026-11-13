package netsec

import (
	"fmt"
	"sort"
)

// Config fixes the caller-supplied initial state and search bounds.
//
// InitialBalance is the account's committed balance used both as the attack
// threshold and as the balance of the entry frame. ContractCash is the cash
// held by the contract at the start of the transaction. WithdrawalAmounts
// contains at most MaxWithdrawalKinds distinct integer amounts in [0,255];
// each call frame independently selects one of them.
type Config struct {
	InitialBalance    int
	ContractCash      int
	WithdrawalAmounts []int
	MaxDepth          int
	MaxSteps          int
}

// DefaultConfig returns a Config with the task's fixed depth and step limits.
func DefaultConfig(initialBalance, contractCash int, amounts []int) Config {
	return Config{
		InitialBalance:    initialBalance,
		ContractCash:      contractCash,
		WithdrawalAmounts: amounts,
		MaxDepth:          MaxCallDepth,
		MaxSteps:          MaxInstructionSteps,
	}
}

// Validate checks the caller-supplied initial state and search bounds without
// changing the Config.
func (c Config) Validate() error {
	_, err := c.normalized()
	return err
}

func (c Config) normalized() (Config, error) {
	return c.normalize(true)
}

func (c Config) normalizedForExecution() (Config, error) {
	return c.normalize(false)
}

func (c Config) normalize(requireAmounts bool) (Config, error) {
	if c.InitialBalance < 0 || c.InitialBalance > MaxAmount {
		return Config{}, fmt.Errorf("initial account balance %d outside [0,%d]", c.InitialBalance, MaxAmount)
	}
	if c.ContractCash < 0 || c.ContractCash > MaxAmount {
		return Config{}, fmt.Errorf("contract cash %d outside [0,%d]", c.ContractCash, MaxAmount)
	}
	if c.MaxDepth == 0 {
		c.MaxDepth = MaxCallDepth
	}
	if c.MaxDepth < 1 || c.MaxDepth > MaxCallDepth {
		return Config{}, fmt.Errorf("maximum depth %d outside [1,%d]", c.MaxDepth, MaxCallDepth)
	}
	if c.MaxSteps == 0 {
		c.MaxSteps = MaxInstructionSteps
	}
	if c.MaxSteps < 1 || c.MaxSteps > MaxInstructionSteps {
		return Config{}, fmt.Errorf("maximum steps %d outside [1,%d]", c.MaxSteps, MaxInstructionSteps)
	}
	if len(c.WithdrawalAmounts) == 0 {
		if !requireAmounts {
			return c, nil
		}
		return Config{}, fmt.Errorf("at least one withdrawal amount is required")
	}

	amounts := append([]int(nil), c.WithdrawalAmounts...)
	sort.Ints(amounts)
	uniq := amounts[:0]
	for _, amount := range amounts {
		if amount < 0 || amount > MaxAmount {
			return Config{}, fmt.Errorf("withdrawal amount %d outside [0,%d]", amount, MaxAmount)
		}
		if len(uniq) == 0 || uniq[len(uniq)-1] != amount {
			uniq = append(uniq, amount)
		}
	}
	if len(uniq) > MaxWithdrawalKinds {
		return Config{}, fmt.Errorf("%d distinct withdrawal amounts; maximum is %d", len(uniq), MaxWithdrawalKinds)
	}
	c.WithdrawalAmounts = append([]int(nil), uniq...)
	return c, nil
}

func (c Config) allowsAmount(amount int) bool {
	for _, allowed := range c.WithdrawalAmounts {
		if amount == allowed {
			return true
		}
	}
	return false
}
