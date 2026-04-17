package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadClientsFileNotFound(t *testing.T) {
	t.Parallel()
	// Non-existent interface should return empty clients file
	cf, err := LoadClientsFile("nonexistent")
	if err != nil {
		t.Fatalf("LoadClientsFile should not error on missing file: %v", err)
	}
	if len(cf.Clients) != 0 {
		t.Errorf("expected empty clients list, got %d", len(cf.Clients))
	}
}

func TestAddOrUpdateClientSavesAndLoads(t *testing.T) {
	t.Parallel()
	// Use a unique interface name to avoid conflicts
	iface := "test-clients-" + time.Now().Format("150405")

	// Clean up after test
	t.Cleanup(func() {
		path := ClientsFilePath(iface)
		os.Remove(path)
	})

	cf, _ := LoadClientsFile(iface)

	// Add a client
	entry := ClientEntry{
		Name:         "testphone",
		WGPubKey:     "testkey123",
		WGPrivateKey: "testprivkey",
		MeshIP:       "10.0.0.5",
	}

	if err := cf.AddOrUpdateClient(iface, entry); err != nil {
		t.Fatalf("AddOrUpdateClient failed: %v", err)
	}

	// Reload and verify
	cf2, err := LoadClientsFile(iface)
	if err != nil {
		t.Fatalf("LoadClientsFile failed: %v", err)
	}
	if len(cf2.Clients) != 1 {
		t.Fatalf("expected 1 client, got %d", len(cf2.Clients))
	}
	if cf2.Clients[0].Name != "testphone" {
		t.Errorf("expected name 'testphone', got %q", cf2.Clients[0].Name)
	}
	if cf2.Clients[0].MeshIP != "10.0.0.5" {
		t.Errorf("expected MeshIP '10.0.0.5', got %q", cf2.Clients[0].MeshIP)
	}
}

func TestFindClientByName(t *testing.T) {
	t.Parallel()
	cf := &ClientsFile{
		Clients: []ClientEntry{
			{Name: "phone1", WGPubKey: "key1"},
			{Name: "phone2", WGPubKey: "key2"},
		},
	}

	found := cf.FindClientByName("phone1")
	if found == nil {
		t.Error("expected to find phone1")
	} else if found.WGPubKey != "key1" {
		t.Errorf("expected key1, got %q", found.WGPubKey)
	}

	notFound := cf.FindClientByName("phone3")
	if notFound != nil {
		t.Error("expected phone3 not to be found")
	}
}

func TestLoadClientsIntoStore(t *testing.T) {
	t.Parallel()
	iface := "test-store-" + time.Now().Format("150405")

	t.Cleanup(func() {
		os.Remove(ClientsFilePath(iface))
	})

	// Create a clients file
	cf := &ClientsFile{
		Clients: []ClientEntry{
			{
				Name:     "client1",
				WGPubKey: "testkey1",
				MeshIP:   "10.0.0.10",
			},
		},
	}
	if err := SaveClientsFile(iface, cf); err != nil {
		t.Fatalf("SaveClientsFile failed: %v", err)
	}

	// Load into peer store
	ps := NewPeerStore()
	if err := LoadClientsIntoStore(iface, ps); err != nil {
		t.Fatalf("LoadClientsIntoStore failed: %v", err)
	}

	// Verify peer is static
	if !ps.IsStaticPeer("testkey1") {
		t.Error("client should be loaded as static peer")
	}

	peer, ok := ps.Get("testkey1")
	if !ok {
		t.Fatal("peer not found in store")
	}
	if peer.MeshIP != "10.0.0.10" {
		t.Errorf("expected MeshIP 10.0.0.10, got %q", peer.MeshIP)
	}
	if peer.Hostname != "client1" {
		t.Errorf("expected Hostname client1, got %q", peer.Hostname)
	}
}

func TestClientsFilePermissions(t *testing.T) {
	t.Parallel()
	iface := "test-perms-" + time.Now().Format("150405")

	t.Cleanup(func() {
		os.Remove(ClientsFilePath(iface))
	})

	cf := &ClientsFile{
		Clients: []ClientEntry{
			{Name: "test", WGPubKey: "key", WGPrivateKey: "secret_private_key"},
		},
	}

	if err := SaveClientsFile(iface, cf); err != nil {
		t.Fatalf("SaveClientsFile failed: %v", err)
	}

	// Check file permissions are 0600 (private)
	path := ClientsFilePath(iface)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat failed: %v", err)
	}

	mode := info.Mode().Perm()
	if mode != 0600 {
		t.Errorf("expected file mode 0600, got %#o", mode)
	}
}

func TestClientDirectoryCreation(t *testing.T) {
	t.Parallel()
	iface := "test-dir-" + time.Now().Format("150405")

	t.Cleanup(func() {
		dir := filepath.Dir(ClientsFilePath(iface))
		os.RemoveAll(dir)
	})

	cf := &ClientsFile{
		Clients: []ClientEntry{{Name: "test", WGPubKey: "key"}},
	}

	if err := SaveClientsFile(iface, cf); err != nil {
		t.Fatalf("SaveClientsFile failed: %v", err)
	}

	// Verify file exists
	path := ClientsFilePath(iface)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("clients file was not created: %v", err)
	}
}
