package ldapfilter_test

import (
	"fmt"

	ldapfilter "example.org/ldapfilter"
)

func ExampleEscapeValue() {
	userInput := "admin)(|(objectClass=*"

	filter, err := ldapfilter.NewEquality("cn", userInput)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	rendered, err := ldapfilter.Build(filter)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(rendered)

	parsed, err := ldapfilter.Parse(rendered)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(parsed.String())
	fmt.Println(parsed.(*ldapfilter.EqualityFilter).Value)
	// Output:
	// (cn=admin\29\28|\28objectClass=\2a)
	// (cn=admin\29\28|\28objectClass=\2a)
	// admin)(|(objectClass=*
}
