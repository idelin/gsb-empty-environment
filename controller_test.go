package netsec

import (
	"errors"
	"testing"
)

var (
	testEnergyObject = ObjectID{
		ClassID: 1,
		OBIS:    [6]byte{0, 0, 1, 0, 0, 255},
	}
	testDisconnectObject = ObjectID{
		ClassID: ClassDisconnectControl,
		OBIS:    [6]byte{0, 0, 96, 3, 10, 255},
	}
)

func newTestController(t *testing.T) *AccessController {
	t.Helper()

	controller, err := NewWithConfig(Config{
		HLSSecret:         []byte("meter-hls-secret"),
		LLSPassword:       []byte("meter-lls-password"),
		RegisteredObjects: []ObjectID{testEnergyObject},
	})
	if err != nil {
		t.Fatalf("NewWithConfig() error = %v", err)
	}
	return controller
}

func assertAuthorize(t *testing.T, controller *AccessController, request AccessRequest, want error) {
	t.Helper()

	got := controller.Authorize(request)
	if !errors.Is(got, want) {
		t.Fatalf("Authorize(%+v) error = %v, want %v", request, got, want)
	}
}

func TestPublicClientCanOnlyReadRegisteredObjects(t *testing.T) {
	controller := newTestController(t)
	if err := controller.Associate(SAPPublic); err != nil {
		t.Fatalf("Associate(SAPPublic) error = %v", err)
	}

	assertAuthorize(t, controller, AccessRequest{
		SAP:    SAPPublic,
		Object: testEnergyObject,
		Access: AccessRead,
	}, nil)

	assertAuthorize(t, controller, AccessRequest{
		SAP:    SAPPublic,
		Object: ObjectID{ClassID: 1, OBIS: [6]byte{0, 0, 2, 0, 0, 255}},
		Access: AccessRead,
	}, ErrObjectNotRegistered)

	assertAuthorize(t, controller, AccessRequest{
		SAP:    SAPPublic,
		Object: testEnergyObject,
		Access: AccessWrite,
	}, ErrAccessDenied)

	assertAuthorize(t, controller, AccessRequest{
		SAP:    SAPPublic,
		Object: testDisconnectObject,
		Access: AccessAction,
		Method: MethodRemoteDisconnect,
	}, ErrAccessDenied)
}

func TestManagementDisconnectActionRequiresCompleteHLS(t *testing.T) {
	controller := newTestController(t)
	if err := controller.Associate(SAPManagement); err != nil {
		t.Fatalf("Associate(SAPManagement) error = %v", err)
	}

	assertAuthorize(t, controller, AccessRequest{
		SAP:    SAPManagement,
		Object: testDisconnectObject,
		Access: AccessAction,
		Method: MethodRemoteDisconnect,
	}, ErrHLSRequired)

	challenge, err := controller.StartHLS(SAPManagement)
	if err != nil {
		t.Fatalf("StartHLS() error = %v", err)
	}
	if len(challenge) != 16 {
		t.Fatalf("challenge length = %d, want 16", len(challenge))
	}
	if state := controller.State(SAPManagement); !state.HLSChallengePending || state.HLSComplete {
		t.Fatalf("state after StartHLS() = %+v", state)
	}

	assertAuthorize(t, controller, AccessRequest{
		SAP:    SAPManagement,
		Object: testDisconnectObject,
		Access: AccessAction,
		Method: MethodRemoteDisconnect,
	}, ErrHLSRequired)

	if err := controller.CompleteHLS(SAPManagement, []byte("not-the-proof")); !errors.Is(err, ErrHLSResponseInvalid) {
		t.Fatalf("CompleteHLS() invalid response error = %v", err)
	}
	assertAuthorize(t, controller, AccessRequest{
		SAP:    SAPManagement,
		Object: testDisconnectObject,
		Access: AccessAction,
		Method: MethodRemoteDisconnect,
	}, ErrHLSRequired)

	challenge, err = controller.StartHLS(SAPManagement)
	if err != nil {
		t.Fatalf("second StartHLS() error = %v", err)
	}
	response := HLSResponse([]byte("meter-hls-secret"), challenge)
	if err := controller.CompleteHLS(SAPManagement, response); err != nil {
		t.Fatalf("CompleteHLS() error = %v", err)
	}

	assertAuthorize(t, controller, AccessRequest{
		SAP:    SAPManagement,
		Object: testDisconnectObject,
		Access: AccessAction,
		Method: MethodRemoteDisconnect,
	}, nil)
	assertAuthorize(t, controller, AccessRequest{
		SAP:    SAPManagement,
		Object: testDisconnectObject,
		Access: AccessAction,
		Method: MethodRemoteReconnect,
	}, nil)
	assertAuthorize(t, controller, AccessRequest{
		SAP:    SAPManagement,
		Object: testDisconnectObject,
		Access: AccessAction,
		Method: 3,
	}, ErrMethodNotAllowed)

	if err := controller.CompleteHLS(SAPManagement, response); !errors.Is(err, ErrHLSChallengeRequired) {
		t.Fatalf("replayed HLS response error = %v, want %v", err, ErrHLSChallengeRequired)
	}
}

func TestEmptyLLSPasswordIsRejectedAndDoesNotSatisfyHLS(t *testing.T) {
	controller := newTestController(t)

	if err := controller.AuthenticateLLS(SAPManagement, nil); !errors.Is(err, ErrEmptyPassword) {
		t.Fatalf("AuthenticateLLS(empty, unassociated) error = %v, want %v", err, ErrEmptyPassword)
	}
	if err := controller.AssociateLLS(SAPManagement, nil); !errors.Is(err, ErrEmptyPassword) {
		t.Fatalf("AssociateLLS(empty) error = %v, want %v", err, ErrEmptyPassword)
	}
	if !controller.IsAssociated(SAPManagement) {
		t.Fatal("empty LLS password unexpectedly removed the association")
	}
	if controller.IsLLSComplete(SAPManagement) || controller.IsHLSComplete(SAPManagement) {
		t.Fatal("empty LLS password established authentication state")
	}

	assertAuthorize(t, controller, AccessRequest{
		SAP:    SAPManagement,
		Object: testDisconnectObject,
		Access: AccessAction,
		Method: MethodRemoteDisconnect,
	}, ErrHLSRequired)

	if err := controller.AuthenticateLLS(SAPManagement, []byte("wrong")); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("AuthenticateLLS(wrong) error = %v, want %v", err, ErrInvalidPassword)
	}
	if err := controller.AuthenticateLLS(SAPManagement, []byte("meter-lls-password")); err != nil {
		t.Fatalf("AuthenticateLLS(correct) error = %v", err)
	}
	if !controller.IsLLSComplete(SAPManagement) {
		t.Fatal("valid LLS password was not recorded")
	}

	assertAuthorize(t, controller, AccessRequest{
		SAP:    SAPManagement,
		Object: testDisconnectObject,
		Access: AccessAction,
		Method: MethodRemoteDisconnect,
	}, ErrHLSRequired)
}

func TestReleaseAndReassociationClearAuthenticatedState(t *testing.T) {
	controller := newTestController(t)
	if err := controller.Associate(SAPManagement); err != nil {
		t.Fatalf("Associate() error = %v", err)
	}

	challenge, err := controller.StartHLS(SAPManagement)
	if err != nil {
		t.Fatalf("StartHLS() error = %v", err)
	}
	response := HLSResponse([]byte("meter-hls-secret"), challenge)
	if err := controller.CompleteHLS(SAPManagement, response); err != nil {
		t.Fatalf("CompleteHLS() error = %v", err)
	}

	disconnectRequest := AccessRequest{
		SAP:    SAPManagement,
		Object: testDisconnectObject,
		Access: AccessAction,
		Method: MethodRemoteDisconnect,
	}
	assertAuthorize(t, controller, disconnectRequest, nil)

	if err := controller.Release(SAPManagement); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if state := controller.State(SAPManagement); state != (AssociationState{}) {
		t.Fatalf("state after Release() = %+v, want zero state", state)
	}
	assertAuthorize(t, controller, disconnectRequest, ErrNotAssociated)

	if err := controller.Associate(SAPManagement); err != nil {
		t.Fatalf("reassociate error = %v", err)
	}
	if controller.IsHLSComplete(SAPManagement) {
		t.Fatal("re-association preserved HLS completion")
	}
	assertAuthorize(t, controller, disconnectRequest, ErrHLSRequired)

	if err := controller.CompleteHLS(SAPManagement, response); !errors.Is(err, ErrHLSChallengeRequired) {
		t.Fatalf("stale HLS response after re-association error = %v, want %v", err, ErrHLSChallengeRequired)
	}
}

func TestHLSIsRestrictedToManagementClient(t *testing.T) {
	controller := newTestController(t)
	if err := controller.Associate(SAPPublic); err != nil {
		t.Fatalf("Associate(SAPPublic) error = %v", err)
	}

	if _, err := controller.StartHLS(SAPPublic); !errors.Is(err, ErrHLSNotAllowed) {
		t.Fatalf("StartHLS(SAPPublic) error = %v, want %v", err, ErrHLSNotAllowed)
	}
	if err := controller.AuthenticateLLS(SAPPublic, []byte("password")); !errors.Is(err, ErrLLSNotAllowed) {
		t.Fatalf("AuthenticateLLS(SAPPublic) error = %v, want %v", err, ErrLLSNotAllowed)
	}
}
