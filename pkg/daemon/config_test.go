package daemon

import (
	"encoding/base64"
	"runtime"
	"testing"
)

const testConfigSecret = "wgmesh-test-secret-long-enough-for-key-derivation"

func TestNewConfigLANDiscoveryDefaultEnabled(t *testing.T) {
	cfg, err := NewConfig(DaemonOpts{Secret: testConfigSecret})
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}

	if !cfg.LANDiscovery {
		t.Fatal("expected LANDiscovery to be enabled by default")
	}
}

func TestNewConfigDisableLANDiscovery(t *testing.T) {
	cfg, err := NewConfig(DaemonOpts{
		Secret:              testConfigSecret,
		DisableLANDiscovery: true,
	})
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}

	if cfg.LANDiscovery {
		t.Fatal("expected LANDiscovery to be disabled")
	}
}

func TestNewConfigIntroducer(t *testing.T) {
	cfg, err := NewConfig(DaemonOpts{
		Secret:     testConfigSecret,
		Introducer: true,
	})
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}

	if !cfg.Introducer {
		t.Fatal("expected Introducer to be enabled")
	}
}

func TestNewConfigDisableIPv6(t *testing.T) {
	cfg, err := NewConfig(DaemonOpts{
		Secret:      testConfigSecret,
		DisableIPv6: true,
	})
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}

	if !cfg.DisableIPv6 {
		t.Fatal("expected DisableIPv6 to be enabled")
	}
}

func TestNewConfigForceRelayAndNoPunching(t *testing.T) {
	cfg, err := NewConfig(DaemonOpts{
		Secret:          testConfigSecret,
		ForceRelay:      true,
		DisablePunching: true,
	})
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}

	if !cfg.ForceRelay {
		t.Fatal("expected ForceRelay to be enabled")
	}
	if !cfg.DisablePunching {
		t.Fatal("expected DisablePunching to be enabled")
	}
}

func TestNewConfigDefaultInterfaceName(t *testing.T) {
	cfg, err := NewConfig(DaemonOpts{Secret: testConfigSecret})
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}

	expected := DefaultInterface
	if runtime.GOOS == "darwin" {
		expected = DefaultInterfaceDarwin
	}

	if cfg.InterfaceName != expected {
		t.Errorf("expected interface %s on %s, got %s", expected, runtime.GOOS, cfg.InterfaceName)
	}
}

func TestNewConfigExplicitInterfaceName(t *testing.T) {
	// Use an OS-appropriate custom name (macOS requires utunN pattern).
	customName := "custom0"
	if runtime.GOOS == "darwin" {
		customName = "utun99"
	}

	cfg, err := NewConfig(DaemonOpts{
		Secret:        testConfigSecret,
		InterfaceName: customName,
	})
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}

	if cfg.InterfaceName != customName {
		t.Errorf("expected interface %s, got %s", customName, cfg.InterfaceName)
	}
}

func TestNewConfigRejectsInvalidInterfaceName(t *testing.T) {
	_, err := NewConfig(DaemonOpts{
		Secret:        testConfigSecret,
		InterfaceName: "../evil",
	})
	if err == nil {
		t.Fatal("expected error for path-traversal interface name")
	}
}

// TestConfig_NoPunchingFlag verifies that the --no-punching flag is correctly
// parsed and stored in the daemon Config.
func TestConfig_NoPunchingFlag(t *testing.T) {
	cfg, err := NewConfig(DaemonOpts{
		Secret:          testConfigSecret,
		DisablePunching: true,
	})
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}

	if !cfg.DisablePunching {
		t.Fatal("expected DisablePunching to be true when --no-punching is set")
	}

	// Verify combining with ForceRelay is also valid (NAT environments may want both)
	cfg2, err := NewConfig(DaemonOpts{
		Secret:          testConfigSecret,
		DisablePunching: true,
		ForceRelay:      true,
	})
	if err != nil {
		t.Fatalf("NewConfig with ForceRelay+DisablePunching failed: %v", err)
	}
	if !cfg2.DisablePunching || !cfg2.ForceRelay {
		t.Fatal("expected both DisablePunching and ForceRelay to be set")
	}
}

// validWGKey returns a syntactically valid 32-byte base64 WireGuard public key.
func validWGKey() string {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i + 1)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestNewConfig_StaticPeerValidation(t *testing.T) {
	t.Parallel()
	goodKey := validWGKey()

	tests := []struct {
		name    string
		spec    StaticPeerSpec
		wantErr bool
	}{
		{"empty pubkey", StaticPeerSpec{}, true},
		{"bad base64 pubkey", StaticPeerSpec{WGPubKey: "not!!base64"}, true},
		{"pubkey wrong length", StaticPeerSpec{WGPubKey: base64.StdEncoding.EncodeToString([]byte("tooshort"))}, true},
		{"bad endpoint", StaticPeerSpec{WGPubKey: goodKey, Endpoint: "notanendpoint"}, true},
		{"bad cidr", StaticPeerSpec{WGPubKey: goodKey, RoutableNetworks: []string{"notacidr"}}, true},
		{"valid pubkey only", StaticPeerSpec{WGPubKey: goodKey}, false},
		{"valid full spec", StaticPeerSpec{
			WGPubKey:         goodKey,
			Endpoint:         "1.2.3.4:51820",
			MeshIP:           "10.0.0.5",
			Hostname:         "opnsense",
			RoutableNetworks: []string{"192.168.1.0/24", "10.10.0.0/16"},
		}, false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewConfig(DaemonOpts{
				Secret:      testConfigSecret,
				StaticPeers: []StaticPeerSpec{tt.spec},
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("got err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestNewConfig_StaticPeersPassedThrough(t *testing.T) {
	t.Parallel()
	specs := []StaticPeerSpec{
		{WGPubKey: validWGKey(), Endpoint: "5.6.7.8:51820", MeshIP: "10.0.0.10"},
	}
	cfg, err := NewConfig(DaemonOpts{
		Secret:      testConfigSecret,
		StaticPeers: specs,
	})
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if len(cfg.StaticPeers) != 1 {
		t.Errorf("expected 1 static peer in config, got %d", len(cfg.StaticPeers))
	}
	if cfg.StaticPeers[0].MeshIP != "10.0.0.10" {
		t.Errorf("expected MeshIP 10.0.0.10, got %q", cfg.StaticPeers[0].MeshIP)
	}
}
