package netsec_test

import (
	"errors"
	"fmt"

	"example.org/netsec/q0313"
)

func ExampleAuthorizeAgentForwarding() {
	service := netsec.ServiceConfig{AllowAgentForwarding: true}

	certExtensions := map[string]string{netsec.ExtNoAgentForwarding: ""}
	err := netsec.AuthorizeAgentForwarding(certExtensions, service)
	fmt.Println(errors.Is(err, netsec.ErrAgentForwardingDenied))

	var certErr *netsec.CertificateExtensionError
	if errors.As(err, &certErr) {
		fmt.Println(certErr.Extension)
	}

	// Output:
	// true
	// no-agent-forwarding
}

func ExampleAgentForwardingReply() {
	service := netsec.ServiceConfig{AllowAgentForwarding: true}

	// Certificate without the extension: the service configuration decides.
	accept, err := netsec.AgentForwardingReply(map[string]string{}, service)
	fmt.Printf("accept=%v err=%v\n", accept, err)

	// Certificate with no-agent-forwarding: always rejected, even when the
	// service itself permits forwarding.
	accept, err = netsec.AgentForwardingReply(
		map[string]string{netsec.ExtNoAgentForwarding: ""},
		service,
	)
	fmt.Printf("accept=%v denied=%v\n", accept, errors.Is(err, netsec.ErrAgentForwardingDenied))

	// Output:
	// accept=true err=<nil>
	// accept=false denied=true
}
