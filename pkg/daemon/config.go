package daemon

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"os"
	"runtime"
	"strings"

	"github.com/atvirokodosprendimai/wgmesh/pkg/crypto"
)

const (
	URIPrefix              = "wgmesh://"
	URIVersion             = "v1"
	DefaultWGPort          = 51820
	DefaultInterface       = "wg0"
	DefaultInterfaceDarwin = "utun20"
)

// StaticPeerSpec describes an operator-configured peer that does not run the
// wgmesh agent (e.g. OPNsense router, mobile device). All fields except
// WGPubKey are optional; an empty Endpoint means the peer only accepts
// inbound WireGuard connections.
type StaticPeerSpec struct {
	WGPubKey         string   // required: 32-byte base64 WireGuard public key
	Endpoint         string   // optional: ip:port of the peer's WireGuard listener
	MeshIP           string   // optional: mesh IPv4 address (e.g. "10.42.0.5")
	MeshIPv6         string   // optional: mesh IPv6 address
	RoutableNetworks []string // optional: LAN CIDRs behind this peer to route
	Hostname         string   // optional: human-readable label for logs/status
}

// Config holds all derived configuration for the mesh daemon
type Config struct {
	Secret          string
	Keys            *crypto.DerivedKeys
	InterfaceName   string
	WGListenPort    int
	AdvertiseRoutes []string
	LogLevel        string
	Privacy         bool
	Gossip          bool
	LANDiscovery    bool
	Introducer      bool
	DisableIPv6     bool
	ForceRelay      bool
	DisablePunching bool
	CustomSubnet    *net.IPNet      // User-specified mesh subnet (nil = use derived)
	StaticPeers     []StaticPeerSpec
}

// DaemonOpts holds options for the daemon
type DaemonOpts struct {
	Secret              string
	InterfaceName       string
	WGListenPort        int
	AdvertiseRoutes     []string
	LogLevel            string
	Privacy             bool
	Gossip              bool
	DisableLANDiscovery bool
	Introducer          bool
	DisableIPv6         bool
	ForceRelay          bool
	DisablePunching     bool
	MeshSubnet          string // Custom mesh subnet CIDR (e.g. "192.168.100.0/24")
	StaticPeers         []StaticPeerSpec
}

// NewConfig creates a new daemon configuration from options
func NewConfig(opts DaemonOpts) (*Config, error) {
	// Parse secret from URI format if needed
	secret := parseSecret(opts.Secret)

	// Warn if secret looks user-chosen rather than auto-generated.
	if opts.Secret != "" && !strings.HasPrefix(strings.TrimSpace(opts.Secret), "wgmesh://") {
		log.Printf("[WARN] Secret does not use wgmesh:// format — it may have low entropy. " +
			"Use 'wgmesh init --secret' to generate a cryptographically strong secret.")
	}

	// Derive all keys
	keys, err := crypto.DeriveKeys(secret)
	if err != nil {
		return nil, fmt.Errorf("failed to derive keys: %w", err)
	}

	// Validate interface name before applying defaults.
	if err := ValidateInterfaceName(opts.InterfaceName); err != nil {
		return nil, fmt.Errorf("invalid interface name: %w", err)
	}

	// Set defaults
	ifaceName := opts.InterfaceName
	if ifaceName == "" {
		if runtime.GOOS == "darwin" {
			ifaceName = DefaultInterfaceDarwin
		} else {
			ifaceName = DefaultInterface
		}
	}

	listenPort := opts.WGListenPort
	if listenPort == 0 {
		listenPort = DefaultWGPort
	}

	logLevel := opts.LogLevel
	if logLevel == "" {
		logLevel = "info"
	}

	// Parse and validate custom subnet if provided
	customSubnet, err := crypto.ParseSubnetOrDefault(opts.MeshSubnet)
	if err != nil {
		return nil, fmt.Errorf("invalid mesh subnet: %w", err)
	}
	if customSubnet != nil {
		if customSubnet.IP.To4() == nil {
			return nil, fmt.Errorf("mesh subnet must be an IPv4 CIDR, got %q", customSubnet.String())
		}
		ones, bits := customSubnet.Mask.Size()
		if bits != 32 {
			return nil, fmt.Errorf("mesh subnet must be an IPv4 CIDR, got %q", customSubnet.String())
		}
		if bits-ones < 2 {
			return nil, fmt.Errorf("mesh subnet /%d is too small (need at least /30 for 2 host addresses)", ones)
		}
	}

	if err := validateStaticPeers(opts.StaticPeers); err != nil {
		return nil, fmt.Errorf("invalid static peer configuration: %w", err)
	}

	return &Config{
		Secret:          secret,
		Keys:            keys,
		InterfaceName:   ifaceName,
		WGListenPort:    listenPort,
		AdvertiseRoutes: opts.AdvertiseRoutes,
		LogLevel:        logLevel,
		Privacy:         opts.Privacy,
		Gossip:          opts.Gossip,
		LANDiscovery:    !opts.DisableLANDiscovery,
		Introducer:      opts.Introducer,
		DisableIPv6:     opts.DisableIPv6,
		ForceRelay:      opts.ForceRelay,
		DisablePunching: opts.DisablePunching,
		CustomSubnet:    customSubnet,
		StaticPeers:     opts.StaticPeers,
	}, nil
}

// validateStaticPeers checks each static peer spec for a valid WireGuard
// public key and, if provided, a valid endpoint and CIDR list.
func validateStaticPeers(specs []StaticPeerSpec) error {
	for i, s := range specs {
		if s.WGPubKey == "" {
			return fmt.Errorf("static peer [%d]: WGPubKey is required", i)
		}
		decoded, err := base64.StdEncoding.DecodeString(s.WGPubKey)
		if err != nil || len(decoded) != 32 {
			return fmt.Errorf("static peer [%d] %q: WGPubKey must be a 32-byte base64 WireGuard public key", i, s.WGPubKey)
		}
		if s.Endpoint != "" {
			if _, _, err := net.SplitHostPort(s.Endpoint); err != nil {
				return fmt.Errorf("static peer [%d] %q: invalid endpoint %q: %w", i, s.WGPubKey, s.Endpoint, err)
			}
		}
		for _, cidr := range s.RoutableNetworks {
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				return fmt.Errorf("static peer [%d] %q: invalid routable network %q: %w", i, s.WGPubKey, cidr, err)
			}
		}
	}
	return nil
}

// PrefixLen returns the prefix length for the mesh subnet.
// Uses CustomSubnet mask if set, otherwise defaults to 16.
func (c *Config) PrefixLen() int {
	if c.CustomSubnet != nil {
		ones, _ := c.CustomSubnet.Mask.Size()
		return ones
	}
	return 16
}

// GenerateSecret generates a new random mesh secret
func GenerateSecret() (string, error) {
	// Generate 32 random bytes
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	// Encode as base64url (no padding for cleaner URLs)
	secret := base64.RawURLEncoding.EncodeToString(b)

	return secret, nil
}

// FormatSecretURI formats a secret as a wgmesh:// URI
func FormatSecretURI(secret string) string {
	return fmt.Sprintf("%s%s/%s", URIPrefix, URIVersion, secret)
}

// ReloadConfigPath returns the path of the reload config file for the given
// interface name.  The file is written by the operator (or systemd service)
// and contains lines of the form KEY=VALUE.  Currently supported keys:
//
//	advertise-routes   comma-separated CIDR list
//	log-level          debug|info|warn|error
func ReloadConfigPath(ifaceName string) string {
	return fmt.Sprintf("/var/lib/wgmesh/%s.reload", ifaceName)
}

// LoadReloadFile parses a reload config file and returns a DaemonOpts with
// only the reloadable fields populated.  Missing or malformed keys are
// silently skipped so that a partial file is still useful.
func LoadReloadFile(path string) (DaemonOpts, error) {
	f, err := os.Open(path)
	if err != nil {
		return DaemonOpts{}, fmt.Errorf("open reload file: %w", err)
	}
	defer f.Close()

	var opts DaemonOpts
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.ToLower(key))
		val = strings.TrimSpace(val)
		switch key {
		case "advertise-routes":
			if val == "" {
				opts.AdvertiseRoutes = []string{}
			} else {
				parts := strings.Split(val, ",")
				routes := make([]string, 0, len(parts))
				for _, p := range parts {
					if r := strings.TrimSpace(p); r != "" {
						routes = append(routes, r)
					}
				}
				opts.AdvertiseRoutes = routes
			}
		case "log-level":
			opts.LogLevel = val
		}
	}
	if err := sc.Err(); err != nil {
		return DaemonOpts{}, fmt.Errorf("read reload file: %w", err)
	}
	return opts, nil
}

// parseSecret extracts the raw secret from various input formats
func parseSecret(input string) string {
	input = strings.TrimSpace(input)

	// Handle wgmesh://v1/secret format
	if strings.HasPrefix(input, URIPrefix) {
		input = strings.TrimPrefix(input, URIPrefix)
		parts := strings.SplitN(input, "/", 2)
		if len(parts) == 2 {
			// Remove query params if present
			secret := parts[1]
			if idx := strings.Index(secret, "?"); idx != -1 {
				secret = secret[:idx]
			}
			return secret
		}
		return parts[0]
	}

	return input
}
