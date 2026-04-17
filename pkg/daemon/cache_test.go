package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPeerCacheSaveAndLoad(t *testing.T) {
	// Use temp directory
	tmpDir := t.TempDir()
	interfaceName := "test-wg0"
	cachePath := filepath.Join(tmpDir, interfaceName+"-peers.json")

	// Create a peer store with some peers
	ps := NewPeerStore()
	ps.Update(&PeerInfo{
		WGPubKey: "pubkey1",
		MeshIP:   "10.0.0.1",
		Endpoint: "1.2.3.4:51820",
	}, "test")
	ps.Update(&PeerInfo{
		WGPubKey:         "pubkey2",
		MeshIP:           "10.0.0.2",
		Endpoint:         "5.6.7.8:51820",
		RoutableNetworks: []string{"192.168.0.0/24"},
	}, "test")

	// Save cache to custom path
	cache := &PeerCache{
		UpdatedAt: time.Now().Unix(),
	}
	for _, p := range ps.GetAll() {
		cache.Peers = append(cache.Peers, PeerCacheEntry{
			WGPubKey:         p.WGPubKey,
			MeshIP:           p.MeshIP,
			Endpoint:         p.Endpoint,
			RoutableNetworks: p.RoutableNetworks,
			LastSeen:         p.LastSeen.Unix(),
		})
	}

	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal cache: %v", err)
	}

	if err := os.WriteFile(cachePath, data, 0600); err != nil {
		t.Fatalf("Failed to write cache: %v", err)
	}

	// Load cache
	loaded, err := loadCacheFromPath(cachePath)
	if err != nil {
		t.Fatalf("Failed to load cache: %v", err)
	}

	if len(loaded.Peers) != 2 {
		t.Errorf("Expected 2 cached peers, got %d", len(loaded.Peers))
	}
}

// loadCacheFromPath loads a cache from a specific path (helper for testing)
func loadCacheFromPath(path string) (*PeerCache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cache PeerCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}

	return &cache, nil
}

func TestCacheExpiration(t *testing.T) {
	ps := NewPeerStore()

	// Add a peer with an old timestamp
	ps.mu.Lock()
	ps.peers["old-key"] = &PeerInfo{
		WGPubKey: "old-key",
		MeshIP:   "10.0.0.1",
		LastSeen: time.Now().Add(-25 * time.Hour), // 25 hours ago, expired
	}
	ps.peers["new-key"] = &PeerInfo{
		WGPubKey: "new-key",
		MeshIP:   "10.0.0.2",
		LastSeen: time.Now(),
	}
	ps.mu.Unlock()

	// Get all peers
	all := ps.GetAll()
	if len(all) != 2 {
		t.Errorf("Expected 2 peers, got %d", len(all))
	}

	// Active should only include the new one
	active := ps.GetActive()
	if len(active) != 1 {
		t.Errorf("Expected 1 active peer, got %d", len(active))
	}
}

func TestCachePersistsIsStatic(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	interfaceName := "test-static"

	// Create peer store and add a static peer
	ps := NewPeerStore()
	ps.AddStaticPeer(&PeerInfo{
		WGPubKey: "statickey1",
		MeshIP:   "10.0.0.99",
		Endpoint: "1.2.3.4:51820",
	})

	// Also add a dynamic peer to distinguish
	ps.Update(&PeerInfo{
		WGPubKey: "dynamickey1",
		MeshIP:   "10.0.0.10",
	}, "dht")

	// Monkey-patch cache file path for testing
	oldCacheFilePath := CacheFilePath
	CacheFilePath = func(name string) string {
		return filepath.Join(tmpDir, name+"-peers.json")
	}
	defer func() { CacheFilePath = oldCacheFilePath }()

	// Save cache
	if err := SavePeerCache(interfaceName, ps); err != nil {
		t.Fatalf("SavePeerCache failed: %v", err)
	}

	// Create new peer store and restore
	ps2 := NewPeerStore()
	RestoreFromCache(interfaceName, ps2)

	// Verify static peer is marked as static
	if !ps2.IsStaticPeer("statickey1") {
		t.Error("static peer should be restored as static")
	}

	// Verify dynamic peer is NOT marked as static
	if ps2.IsStaticPeer("dynamickey1") {
		t.Error("dynamic peer should not be marked as static")
	}

	// Verify both peers exist
	if _, ok := ps2.Get("statickey1"); !ok {
		t.Error("static peer should exist in restored store")
	}
	if _, ok := ps2.Get("dynamickey1"); !ok {
		t.Error("dynamic peer should exist in restored store")
	}
}

func TestCacheJsonContainsIsStatic(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	interfaceName := "test-json"

	ps := NewPeerStore()
	ps.AddStaticPeer(&PeerInfo{
		WGPubKey: "statickey",
		MeshIP:   "10.0.0.50",
	})

	oldCacheFilePath := CacheFilePath
	CacheFilePath = func(name string) string {
		return filepath.Join(tmpDir, name+"-peers.json")
	}
	defer func() { CacheFilePath = oldCacheFilePath }()

	if err := SavePeerCache(interfaceName, ps); err != nil {
		t.Fatalf("SavePeerCache failed: %v", err)
	}

	// Read the JSON and verify is_static field is present
	path := filepath.Join(tmpDir, interfaceName+"-peers.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	var cache PeerCache
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if len(cache.Peers) != 1 {
		t.Fatalf("expected 1 peer in cache, got %d", len(cache.Peers))
	}

	if !cache.Peers[0].IsStatic {
		t.Error("expected is_static=true in cached entry")
	}
}
