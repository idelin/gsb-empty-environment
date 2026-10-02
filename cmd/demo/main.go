package main

import (
	"fmt"
	"reflect"
	"strings"

	"exprfmt"
)

const sample = `processOrder(validateCustomer(customerName, customerEmail, customerAddress), calculateSubtotal(itemPrice * itemQuantity, discountPercent * itemPrice, loyaltyPoints / redemptionRate), applyShipping(destinationCountry, packageWeight, packageDimensions, expressDelivery), finalizePayment(cardNumber, expiryDate, securityCode, billingAddress)) && status == "active" || overrideApproved`

func main() {
	ast, err := exprfmt.Parse(sample)
	if err != nil {
		panic(err)
	}
	fmt.Println("=== input (single line) ===")
	fmt.Println(sample)

	formatted := exprfmt.Format(ast)
	fmt.Println("\n=== formatted (80-column limit) ===")
	fmt.Println(formatted)

	max := 0
	for _, line := range strings.Split(formatted, "\n") {
		if len(line) > max {
			max = len(line)
		}
	}
	fmt.Printf("\nlongest line: %d columns\n", max)

	again, err := exprfmt.Parse(formatted)
	if err != nil {
		panic(err)
	}
	fmt.Println("round-trip AST identical:", reflect.DeepEqual(ast, again))
}
