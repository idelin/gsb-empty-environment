package tokenexchange_test

import (
	"context"
	"fmt"
	"time"

	"example.org/netsec/q0428/tokenexchange"
)

type exampleInspector struct{}

func (exampleInspector) InspectToken(_ context.Context, token string) (tokenexchange.TokenInfo, error) {
	if token != "subject.jwt" {
		return tokenexchange.TokenInfo{}, nil
	}
	return tokenexchange.TokenInfo{
		Active:   true,
		Type:     tokenexchange.TypeAccessToken,
		Subject:  "user-42",
		ClientID: "legacy-app",
		Scopes:   []string{"read", "write"},
		Expiry:   time.Now().Add(time.Hour),
		MayAct:   &tokenexchange.Actor{ClientID: "gateway"},
	}, nil
}

func ExampleEngine() {
	policy := tokenexchange.NewAudienceTable(map[string][]string{
		"gateway": {"billing-api"},
	})
	engine := tokenexchange.NewEngine(exampleInspector{}, policy)

	decision, err := engine.Decide(context.Background(), tokenexchange.Request{
		Requester:        tokenexchange.Requester{ClientID: "gateway"},
		SubjectToken:     "subject.jwt",
		SubjectTokenType: tokenexchange.TypeAccessToken,
		Audience:         "billing-api",
		Scopes:           []string{"read"},
	})
	if err != nil {
		fmt.Println("denied:", err)
		return
	}

	fmt.Printf("subject=%s audience=%s scopes=%v delegated=%t\n",
		decision.Subject, decision.Audience, decision.Scopes, decision.Delegated)
	// Output: subject=user-42 audience=billing-api scopes=[read] delegated=false
}
