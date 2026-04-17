package daemon

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ManualConfigPath returns the path to the manual WireGuard config file.
func ManualConfigPath(ifaceName string) string {
	return filepath.Join("/etc/wgmesh", fmt.Sprintf("%s.conf.manual", ifaceName))
}

// ManualPeer represents a validated peer entry from the manual config file.
type ManualPeer struct {
	PubKey     string
	AllowedIPs []string
	Endpoint   string // optional
	PSK        string // optional, base64-encoded
	Relay      bool   // opt-in: use relay routing via local node
}

// ValidationError reports a single validation failure with line and peer context.
type ValidationError struct {
	Line    int    // line number in file (1-indexed)
	Peer    int    // peer block number (1-indexed)
	Field   string // "PublicKey", "AllowedIPs", "Endpoint", "PSK"
	Message string
}

// ParseAndValidateManualConfig parses and validates a manual WireGuard config file.
// Returns (peers, validationErrors, fileReadError).
// If file doesn't exist, returns (nil, nil, os.ErrNotExist).
// If any peer block has validation errors, the whole file is rejected.
func ParseAndValidateManualConfig(path string) ([]ManualPeer, []ValidationError, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	var peers []ManualPeer
	var errors []ValidationError

	scanner := bufio.NewScanner(file)
	lineNum := 0
	var currentPeer struct {
		pubKey     string
		allowedIPs []string
		endpoint   string
		psk        string
		relay      bool
	}
	var peerNum int
	var inPeerBlock bool
	var skipBlock bool

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		line = strings.TrimSpace(line)

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Section headers
		if strings.HasPrefix(line, "[") {
			// Flush previous peer block if any
			if inPeerBlock && currentPeer.pubKey != "" {
				peerNum++
				validationErrors := validateManualPeer(peerNum, currentPeer)
				errors = append(errors, validationErrors...)
				if len(validationErrors) == 0 {
					peers = append(peers, ManualPeer{
						PubKey:     currentPeer.pubKey,
						AllowedIPs: currentPeer.allowedIPs,
						Endpoint:   currentPeer.endpoint,
						PSK:        currentPeer.psk,
						Relay:      currentPeer.relay,
					})
				}
			}

			// Parse new section
			section := strings.Trim(line, "[]")
			if section == "Peer" {
				inPeerBlock = true
				skipBlock = false
				currentPeer = struct {
					pubKey     string
					allowedIPs []string
					endpoint   string
					psk        string
					relay      bool
				}{}
			} else if section == "Interface" {
				inPeerBlock = false
				skipBlock = true
			} else {
				skipBlock = true
			}
			continue
		}

		if skipBlock || !inPeerBlock {
			continue
		}

		// Parse key=value lines
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "PublicKey":
			currentPeer.pubKey = value
		case "AllowedIPs":
			cidrs := strings.Split(value, ",")
			for _, cidr := range cidrs {
				if c := strings.TrimSpace(cidr); c != "" {
					currentPeer.allowedIPs = append(currentPeer.allowedIPs, c)
				}
			}
		case "Endpoint":
			currentPeer.endpoint = value
		case "PresharedKey":
			currentPeer.psk = value
		case "Relay":
			currentPeer.relay = strings.ToLower(value) == "true"
		}
	}

	// Flush final peer block
	if inPeerBlock && currentPeer.pubKey != "" {
		peerNum++
		validationErrors := validateManualPeer(peerNum, currentPeer)
		errors = append(errors, validationErrors...)
		if len(validationErrors) == 0 {
			peers = append(peers, ManualPeer{
				PubKey:     currentPeer.pubKey,
				AllowedIPs: currentPeer.allowedIPs,
				Endpoint:   currentPeer.endpoint,
				PSK:        currentPeer.psk,
				Relay:      currentPeer.relay,
			})
		}
	}

	if scanner.Err() != nil {
		return nil, nil, scanner.Err()
	}

	// Return all peers only if NO validation errors
	if len(errors) > 0 {
		return nil, errors, nil
	}

	return peers, nil, nil
}

func validateManualPeer(peerNum int, peer struct {
	pubKey     string
	allowedIPs []string
	endpoint   string
	psk        string
	relay      bool
}) []ValidationError {
	var errors []ValidationError

	// Validate PublicKey
	if peer.pubKey == "" {
		errors = append(errors, ValidationError{
			Peer:    peerNum,
			Field:   "PublicKey",
			Message: "required field missing",
		})
	} else if !isValidWGKey(peer.pubKey) {
		errors = append(errors, ValidationError{
			Peer:    peerNum,
			Field:   "PublicKey",
			Message: fmt.Sprintf("invalid base64 or wrong length: %s", peer.pubKey),
		})
	}

	// Validate AllowedIPs
	for _, cidr := range peer.allowedIPs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			errors = append(errors, ValidationError{
				Peer:    peerNum,
				Field:   "AllowedIPs",
				Message: fmt.Sprintf("invalid CIDR: %s", cidr),
			})
		}
	}

	// Validate Endpoint (optional)
	if peer.endpoint != "" {
		host, port, err := net.SplitHostPort(peer.endpoint)
		if err != nil {
			errors = append(errors, ValidationError{
				Peer:    peerNum,
				Field:   "Endpoint",
				Message: fmt.Sprintf("invalid host:port format: %s", peer.endpoint),
			})
		} else {
			if port == "" {
				errors = append(errors, ValidationError{
					Peer:    peerNum,
					Field:   "Endpoint",
					Message: fmt.Sprintf("port missing: %s", peer.endpoint),
				})
			} else {
				portNum, err := strconv.ParseUint(port, 10, 16)
				if err != nil || portNum == 0 {
					errors = append(errors, ValidationError{
						Peer:    peerNum,
						Field:   "Endpoint",
						Message: fmt.Sprintf("invalid port: %s", port),
					})
				}
			}
			// Warn if host is not an IP (could be DNS)
			if net.ParseIP(host) == nil {
				// Allow DNS names, just not validating them here
			}
		}
	}

	// Validate PSK (optional, but if present must be valid base64)
	if peer.psk != "" {
		decoded, err := base64.StdEncoding.DecodeString(peer.psk)
		if err != nil || len(decoded) != 32 {
			errors = append(errors, ValidationError{
				Peer:    peerNum,
				Field:   "PSK",
				Message: fmt.Sprintf("invalid base64 or wrong length (must be 32 bytes)"),
			})
		}
	}

	return errors
}

func isValidWGKey(key string) bool {
	decoded, err := base64.StdEncoding.DecodeString(key)
	return err == nil && len(decoded) == 32
}
