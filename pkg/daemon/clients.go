package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// ClientEntry represents a client device created by `wgmesh add-client`.
type ClientEntry struct {
	Name             string   `json:"name"`
	WGPubKey         string   `json:"wg_pubkey"`
	WGPrivateKey     string   `json:"wg_private_key"` // stored so user can re-print config
	MeshIP           string   `json:"mesh_ip"`
	MeshIPv6         string   `json:"mesh_ipv6,omitempty"`
	RoutableNetworks []string `json:"routable_networks,omitempty"`
	CreatedAt        int64    `json:"created_at"`
}

// ClientsFile is the JSON structure for persisting client devices.
type ClientsFile struct {
	Clients   []ClientEntry `json:"clients"`
	UpdatedAt int64         `json:"updated_at"`
}

// ClientsFilePath returns the path for the clients file
func ClientsFilePath(interfaceName string) string {
	return filepath.Join("/var/lib/wgmesh", fmt.Sprintf("%s-clients.json", interfaceName))
}

// LoadClientsFile loads the clients file from disk
func LoadClientsFile(interfaceName string) (*ClientsFile, error) {
	path := ClientsFilePath(interfaceName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ClientsFile{Clients: []ClientEntry{}}, nil
		}
		return nil, fmt.Errorf("failed to read clients file: %w", err)
	}

	var cf ClientsFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("failed to parse clients file: %w", err)
	}

	return &cf, nil
}

// SaveClientsFile saves the clients file to disk with mode 0600
func SaveClientsFile(interfaceName string, cf *ClientsFile) error {
	if cf.Clients == nil {
		cf.Clients = []ClientEntry{}
	}
	cf.UpdatedAt = time.Now().Unix()

	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal clients file: %w", err)
	}

	path := ClientsFilePath(interfaceName)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create clients directory: %w", err)
	}

	return os.WriteFile(path, data, 0600)
}

// LoadClientsIntoStore loads all clients from the clients file into the peer
// store as static peers. Called on daemon startup and on SIGHUP.
func LoadClientsIntoStore(interfaceName string, peerStore *PeerStore) error {
	cf, err := LoadClientsFile(interfaceName)
	if err != nil {
		return err
	}

	for _, client := range cf.Clients {
		peer := &PeerInfo{
			WGPubKey:         client.WGPubKey,
			Hostname:         client.Name,
			MeshIP:           client.MeshIP,
			MeshIPv6:         client.MeshIPv6,
			RoutableNetworks: client.RoutableNetworks,
			LastSeen:         time.Now(),
		}
		peerStore.AddStaticPeer(peer)
	}

	if len(cf.Clients) > 0 {
		log.Printf("[Clients] Loaded %d clients into peer store", len(cf.Clients))
	}

	return nil
}

// FindClientByName returns a client entry by name, or nil if not found
func (cf *ClientsFile) FindClientByName(name string) *ClientEntry {
	for i := range cf.Clients {
		if cf.Clients[i].Name == name {
			return &cf.Clients[i]
		}
	}
	return nil
}

// AddOrUpdateClient adds or updates a client entry, saves to disk
func (cf *ClientsFile) AddOrUpdateClient(interfaceName string, entry ClientEntry) error {
	entry.CreatedAt = time.Now().Unix()

	// Check if client with this name already exists
	for i, c := range cf.Clients {
		if c.Name == entry.Name {
			// Update existing
			cf.Clients[i] = entry
			return SaveClientsFile(interfaceName, cf)
		}
	}

	// Add new
	cf.Clients = append(cf.Clients, entry)
	return SaveClientsFile(interfaceName, cf)
}
