package netsec

import (
	"errors"
	"testing"
)

func TestStateMachinePreservesStateAndPhase(t *testing.T) {
	machine := NewStateMachine("code id_token")

	if machine.Phase() != PhaseIdle {
		t.Fatalf("initial Phase = %q, want %q", machine.Phase(), PhaseIdle)
	}

	result := machine.Apply(ServiceState{ResponseType: "code", State: "csrf-token"})
	if !result.Allowed {
		t.Fatalf("code should be allowed: %v", result.Err)
	}
	if result.State != "csrf-token" {
		t.Fatalf("State = %q, want preserved csrf-token", result.State)
	}
	if result.Phase != PhaseAuthorized || machine.Phase() != PhaseAuthorized {
		t.Fatalf("Phase = %q, machine Phase = %q", result.Phase, machine.Phase())
	}

	frontChannel, err := result.NewFrontChannelResponse("the-code", "")
	if err != nil {
		t.Fatalf("NewFrontChannelResponse: %v", err)
	}
	if frontChannel.State != "csrf-token" {
		t.Fatalf("front-channel State = %q, want preserved csrf-token", frontChannel.State)
	}

	result = machine.Transition(ServiceState{ResponseType: "code token", State: "csrf-token"})
	if result.Allowed {
		t.Fatal("code token must be rejected")
	}
	if !errors.Is(result.Err, ErrAccessTokenInFrontend) {
		t.Fatalf("Err = %v, want ErrAccessTokenInFrontend", result.Err)
	}
	if result.State != "csrf-token" {
		t.Fatalf("State = %q, want preserved csrf-token", result.State)
	}
	if result.Phase != PhaseRejected || machine.Phase() != PhaseRejected {
		t.Fatalf("Phase = %q, machine Phase = %q", result.Phase, machine.Phase())
	}

	machine.Reset()
	if machine.Phase() != PhaseIdle {
		t.Fatalf("Phase after Reset = %q, want %q", machine.Phase(), PhaseIdle)
	}
}

func TestStateMachineUsesServiceRegistrations(t *testing.T) {
	machine := NewStateMachine()

	state := ServiceState{
		ResponseType:            "code id_token",
		RegisteredResponseTypes: []string{"code id_token"},
	}
	if got := machine.Apply(state); !got.Allowed {
		t.Fatalf("service registration should allow hybrid: %v", got.Err)
	}

	state.RegisteredResponseTypes = []string{"code"}
	if got := machine.Apply(state); got.Allowed {
		t.Fatal("empty registration set must reject hybrid")
	}

	state.RegisteredResponseTypes = nil
	machine = NewStateMachine("code id_token")
	if got := machine.Apply(state); !got.Allowed {
		t.Fatalf("nil registrations should use machine default: %v", got.Err)
	}
}

func TestStateMachineNilReceiver(t *testing.T) {
	var machine *StateMachine
	result := machine.Apply(ServiceState{ResponseType: "code", State: "opaque"})
	if result.Allowed {
		t.Fatal("nil machine must reject")
	}
	if result.State != "opaque" {
		t.Fatalf("State = %q, want preserved opaque", result.State)
	}
	if machine.Phase() != PhaseIdle {
		t.Fatalf("nil machine Phase = %q", machine.Phase())
	}
}

func TestFrontChannelResponseNeverCarriesAccessToken(t *testing.T) {
	codeDecision := NewPolicy().Evaluate("code")
	response, err := codeDecision.NewFrontChannelResponse("the-code", "", "the-state")
	if err != nil {
		t.Fatalf("NewFrontChannelResponse: %v", err)
	}
	if response.ResponseMode != ResponseModeQuery {
		t.Fatalf("ResponseMode = %q, want query", response.ResponseMode)
	}
	if response.Code != "the-code" || response.IDToken != "" {
		t.Fatalf("unexpected response: %+v", response)
	}

	params := response.Parameters()
	if params.Get("code") != "the-code" {
		t.Fatalf("code parameter = %q", params.Get("code"))
	}
	if params.Get("state") != "the-state" {
		t.Fatalf("state parameter = %q", params.Get("state"))
	}
	if _, ok := params["access_token"]; ok {
		t.Fatal("front-channel parameters must not contain access_token")
	}
	if _, ok := params["id_token"]; ok {
		t.Fatal("standalone code response must not contain id_token")
	}

	if _, err := codeDecision.NewFrontChannelResponse("the-code", "an-id-token", "the-state"); err == nil {
		t.Fatal("id_token must not be added to a standalone code response")
	}
}

func TestFrontChannelHybridResponseRequiresBothValues(t *testing.T) {
	decision := NewPolicy("code id_token").Evaluate("code id_token")
	if !decision.Allowed {
		t.Fatalf("hybrid should be allowed: %v", decision.Err)
	}

	response, err := decision.NewFrontChannelResponse("the-code", "the-id-token", "the-state")
	if err != nil {
		t.Fatalf("NewFrontChannelResponse: %v", err)
	}
	if response.ResponseMode != ResponseModeFragment {
		t.Fatalf("ResponseMode = %q, want fragment", response.ResponseMode)
	}

	params := response.Parameters()
	if params.Get("code") != "the-code" || params.Get("id_token") != "the-id-token" {
		t.Fatalf("unexpected parameters: %v", params)
	}
	if _, ok := params["access_token"]; ok {
		t.Fatal("front-channel parameters must not contain access_token")
	}

	if _, err := decision.NewFrontChannelResponse("only-code", "", ""); err == nil {
		t.Fatal("hybrid response without id_token must fail")
	}
	if _, err := decision.NewFrontChannelResponse("", "only-id-token", ""); err == nil {
		t.Fatal("hybrid response without code must fail")
	}
}

func TestDeniedDecisionCannotBuildFrontChannelResponse(t *testing.T) {
	decision := NewPolicy().Evaluate("code token")
	if _, err := decision.NewFrontChannelResponse("the-code", "", "the-state"); !errors.Is(err, ErrAccessTokenInFrontend) {
		t.Fatalf("err = %v, want ErrAccessTokenInFrontend", err)
	}
}
