// Package netsec implements the client side of the CIBA ping mode
// (Client Initiated Backchannel Authentication, OIDC CIBA).
//
// In ping mode the authorization server notifies this client at a
// pre-registered notification endpoint. The notification is untrusted by
// itself: it merely says that the authentication request identified by
// auth_req_id now has a result. This package verifies every notification and
// then fetches tokens from the token endpoint, never accepting tokens carried
// in the notification body.
//
// Security properties enforced by the handler:
//   - Only POST requests are accepted.
//   - The Authorization: Bearer value must equal the
//     client_notification_token bound to the auth_req_id, compared in
//     constant time (crypto/subtle).
//   - The auth_req_id must belong to a request initiated by this client and
//     must not have expired; foreign or unknown identifiers are rejected.
//   - A second notification for the same auth_req_id is ignored.
//   - After the first valid notification the token endpoint is queried with
//     the auth_req_id; token fields in the notification body are discarded.
//   - client_notification_token values must be 22..1024 characters long;
//     invalid values are rejected before a backchannel request is made.
//
// Typical usage:
//
//	fetcher := &netsec.HTTPTokenFetcher{
//	    TokenEndpoint: "https://as.example.com/token",
//	    ClientID:      "my-client",
//	    ClientSecret:  "secret",
//	}
//	client := netsec.NewClient(fetcher)
//
//	// When initiating a backchannel authentication request:
//	token := "random-cryptographically-strong-notification-token"
//	if err := netsec.ValidateNotificationToken(token); err != nil {
//	    return err // fix the token before calling the AS
//	}
//	authReqID, expiresIn := startBackchannelRequest(token) // AS response
//	if err := client.PrepareAuthRequest(authReqID, token, expiresIn); err != nil {
//	    return err
//	}
//
//	// Mount the notification endpoint:
//	http.Handle("/ciba-ping", client)
//
//	// After a 204 response the tokens are available via client.Token.
package netsec
