package tests

import (
	"net/http"
	"testing"
	"time"

	"github.com/matrix-org/complement-crypto/internal/api"
)

// If a client cannot query device keys for a user, it retries.
//
// Block the first few requests to /keys/query so device key download fails.
// Create two users and ensure they can send encrypted messages to each other.
// This proves that device keys download requests get retried.
func TestFailedDeviceKeyDownloadRetries(t *testing.T) {
	ForEachClientType(t, func(t *testing.T, clientType api.ClientType) {
		tc := CreateTestContext(t, clientType, clientType)
		// Given that the first 4 attempts to download device keys will fail
		tc.Deployment.WithMITMOptions(t, map[string]interface{}{
			"statuscode": map[string]interface{}{
				"return_status": http.StatusGatewayTimeout,
				"block_request": true,
				"count":         4,
				"filter":        "~u .*\\/keys\\/query.* ~m POST",
			},
		}, func() {
			// And Alice and Bob are in an encrypted room together
			roomID := tc.CreateNewEncryptedRoom(t, tc.Alice, "private_chat", []string{tc.Bob.UserID})
			tc.Bob.MustJoinRoom(t, roomID, []string{"hs1"})

			tc.WithAliceAndBobSyncing(t, func(alice, bob api.Client) {
				// When Alice sends a message
				alice.SendMessage(t, roomID, "checking whether we can send a message")

				// Then Bob should receive it
				bob.WaitUntilEventInRoom(
					t,
					roomID,
					api.CheckEventHasBody("checking whether we can send a message"),
				).Wait(t, 5*time.Second)

			})
		})
	})
}