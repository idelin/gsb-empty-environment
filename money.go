package fxacct

import (
	"fmt"
	"math/big"
)

// Money is an exact amount in a currency. Value is never rounded
// implicitly; quantization happens only at Round pipeline nodes.
type Money struct {
	Value    *big.Rat
	Currency string
}

func (m Money) String() string {
	return fmt.Sprintf("%s %s", FormatValue(m.Value), m.Currency)
}
