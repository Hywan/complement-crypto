package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement-crypto/internal/api"
	"github.com/matrix-org/complement-crypto/internal/api/js"
	"github.com/matrix-org/complement-crypto/internal/api/rust"
	"github.com/matrix-org/complement-crypto/internal/config"
	"github.com/matrix-org/complement-crypto/internal/deploy"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/must"
)

// globals to ensure we are always referring to the same set of HSes/proxies between tests
var (
	ssDeployment           *deploy.SlidingSyncDeployment
	ssMutex                *sync.Mutex
	complementCryptoConfig *config.ComplementCrypto // set in TestMain
)

// Main entry point when users run `go test`. Defined in https://pkg.go.dev/testing#hdr-Main
func TestMain(m *testing.M) {
	complementCryptoConfig = config.NewComplementCryptoConfigFromEnvVars()
	ssMutex = &sync.Mutex{}

	// nuke persistent storage from previous run. We do this on startup rather than teardown
	// to allow devs to introspect DBs / Chrome profiles if tests fail.
	// TODO: ideally client packages would do this.
	os.RemoveAll("./rust_storage")
	os.RemoveAll("./chromedp")

	files, _ := os.ReadDir("./logs")
	for _, f := range files {
		if strings.HasPrefix(f.Name(), "rust_sdk_logs") {
			os.Remove(filepath.Join("./logs", f.Name()))
		}
	}
	js.SetupJSLogs("./logs/js_sdk.log")                  // rust sdk logs on its own
	complement.TestMainWithCleanup(m, "crypto", func() { // always teardown even if panicking
		ssMutex.Lock()
		if ssDeployment != nil {
			ssDeployment.Teardown(complementCryptoConfig.WriteContainerLogs)
		}
		ssMutex.Unlock()
		js.WriteJSLogs()
	})
}

// Deploy a new network of HSes. If Deploy has been called before, returns the existing
// deployment.
func Deploy(t *testing.T) *deploy.SlidingSyncDeployment {
	ssMutex.Lock()
	defer ssMutex.Unlock()
	if ssDeployment != nil {
		return ssDeployment
	}
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to find working directory: %s", err)
	}
	mitmProxyAddonsDir := filepath.Join(workingDir, "mitmproxy_addons")
	ssDeployment = deploy.RunNewDeployment(t, mitmProxyAddonsDir, complementCryptoConfig.TCPDump)
	return ssDeployment
}

// ClientTypeMatrix enumerates all provided client permutations given by the test client
// matrix `COMPLEMENT_CRYPTO_TEST_CLIENT_MATRIX`. Creates sub-tests for each permutation
// and invokes `subTest`. Sub-tests are run in series.
func ClientTypeMatrix(t *testing.T, subTest func(tt *testing.T, a, b api.ClientType)) {
	for _, tc := range complementCryptoConfig.TestClientMatrix {
		tc := tc
		t.Run(fmt.Sprintf("%s|%s", tc[0], tc[1]), func(t *testing.T) {
			subTest(t, tc[0], tc[1])
		})
	}
}

// ForEachClientType enumerates all known client implementations and creates sub-tests for
// each. Sub-tests are run in series. Always defaults to `hs1`.
func ForEachClientType(t *testing.T, subTest func(tt *testing.T, a api.ClientType)) {
	for _, tc := range []api.ClientType{{Lang: api.ClientTypeRust, HS: "hs1"}, {Lang: api.ClientTypeJS, HS: "hs1"}} {
		tc := tc
		t.Run(string(tc.Lang), func(t *testing.T) {
			subTest(t, tc)
		})
	}
}

// MustCreateClient creates an api.Client with the specified language/server, else fails the test.
//
// Options can be provided to configure clients, such as enabling persistent storage.
func MustCreateClient(t *testing.T, clientType api.ClientType, cfg api.ClientCreationOpts, ssURL string, opts ...func(api.Client, *api.ClientCreationOpts)) api.Client {
	var c api.Client
	switch clientType.Lang {
	case api.ClientTypeRust:
		client, err := rust.NewRustClient(t, cfg, ssURL)
		must.NotError(t, "NewRustClient: %s", err)
		c = client
	case api.ClientTypeJS:
		client, err := js.NewJSClient(t, cfg)
		must.NotError(t, "NewJSClient: %s", err)
		c = client
	default:
		t.Fatalf("unknown client type %v", clientType)
	}
	for _, o := range opts {
		o(c, &cfg)
	}
	return c
}

// WithDoLogin is an option which can be provided to MustCreateClient which will automatically login, else fail the test.
func WithDoLogin(t *testing.T) func(api.Client, *api.ClientCreationOpts) {
	return func(c api.Client, opts *api.ClientCreationOpts) {
		must.NotError(t, "failed to login", c.Login(t, *opts))
	}
}

// WithPersistentStorage is an option which can be provided to MustCreateClient which will configure clients to use persistent storage,
// e.g IndexedDB or sqlite3 files.
func WithPersistentStorage() func(*api.ClientCreationOpts) {
	return func(o *api.ClientCreationOpts) {
		o.PersistentStorage = true
	}
}

// TestContext provides a consistent set of variables which most tests will need access to.
type TestContext struct {
	Deployment *deploy.SlidingSyncDeployment
	// Alice is defined if at least 1 clientType is provided to CreateTestContext.
	Alice *client.CSAPI
	// Bob is defined if at least 2 clientTypes are provided to CreateTestContext.
	Bob *client.CSAPI
	// Charlie is defined if at least 3 clientTypes are provided to CreateTestContext.
	Charlie *client.CSAPI
}

// CreateTestContext creates a new test context suitable for immediate use. The variadic clientTypes
// control how many clients are automatically registered.
func CreateTestContext(t *testing.T, clientType ...api.ClientType) *TestContext {
	deployment := Deploy(t)
	tc := &TestContext{
		Deployment: deployment,
	}
	// pre-register alice and bob, if told
	if len(clientType) > 0 {
		tc.Alice = deployment.Register(t, clientType[0].HS, helpers.RegistrationOpts{
			LocalpartSuffix: "alice",
			Password:        "complement-crypto-password",
		})
	}
	if len(clientType) > 1 {
		tc.Bob = deployment.Register(t, clientType[1].HS, helpers.RegistrationOpts{
			LocalpartSuffix: "bob",
			Password:        "complement-crypto-password",
		})
	}
	if len(clientType) > 2 {
		tc.Charlie = deployment.Register(t, clientType[1].HS, helpers.RegistrationOpts{
			LocalpartSuffix: "charlie",
			Password:        "complement-crypto-password",
		})
	}
	if len(clientType) > 3 {
		t.Fatalf("CreateTestContext: too many clients: got %d", len(clientType))
	}
	return tc
}

// CreateNewEncryptedRoom calls creator.MustCreateRoom with the correct m.room.encryption state event.
func (c *TestContext) CreateNewEncryptedRoom(t *testing.T, creator *client.CSAPI, preset string, invite []string) (roomID string) {
	t.Helper()
	if invite == nil {
		invite = []string{} // else synapse 500s
	}
	return creator.MustCreateRoom(t, map[string]interface{}{
		"name":   t.Name(),
		"preset": preset,
		"invite": invite,
		"initial_state": []map[string]interface{}{
			{
				"type":      "m.room.encryption",
				"state_key": "",
				"content": map[string]interface{}{
					"algorithm": "m.megolm.v1.aes-sha2",
				},
			},
		},
	})
}

// OptsFromClient converts a Complement client into a set of options which can be used to create an api.Client.
func (c *TestContext) OptsFromClient(t *testing.T, existing *client.CSAPI, options ...func(*api.ClientCreationOpts)) api.ClientCreationOpts {
	o := &api.ClientCreationOpts{
		BaseURL:  existing.BaseURL,
		UserID:   existing.UserID,
		DeviceID: existing.DeviceID,
		Password: existing.Password,
	}
	for _, opt := range options {
		opt(o)
	}
	return *o
}

// MustRegisterNewDevice logs in a new device for this client, else fails the test.
func (c *TestContext) MustRegisterNewDevice(t *testing.T, cli *client.CSAPI, hsName, newDeviceID string) *client.CSAPI {
	return c.Deployment.Login(t, hsName, cli, helpers.LoginOpts{
		DeviceID: newDeviceID,
		Password: cli.Password,
	})
}

// MustCreateClient creates an api.Client from an existing Complement client and the specified client type. Additional options
// can be set to configure the client beyond that of the Complement client e.g to add persistent storage.
func (c *TestContext) MustCreateClient(t *testing.T, cli *client.CSAPI, clientType api.ClientType, options ...func(*api.ClientCreationOpts)) api.Client {
	t.Helper()
	cfg := api.NewClientCreationOpts(cli)
	cfg.BaseURL = c.Deployment.ReverseProxyURLForHS(clientType.HS)
	for _, opt := range options {
		opt(&cfg)
	}
	client := MustCreateClient(t, clientType, cfg, c.Deployment.SlidingSyncURL(t))
	return client
}

// MustLoginClient is the same as MustCreateClient but also logs in the client. TODO REMOVE
func (c *TestContext) MustLoginClient(t *testing.T, cli *client.CSAPI, clientType api.ClientType, options ...func(*api.ClientCreationOpts)) api.Client {
	t.Helper()
	client := c.MustCreateClient(t, cli, clientType, options...)
	must.NotError(t, "failed to login client", client.Login(t, client.Opts()))
	return client
}
