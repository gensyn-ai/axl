package api

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)

type stubTopology struct {
	peers []core.PeerInfo
	tree  []core.TreeEntryInfo
	ip    net.IP
	key   ed25519.PublicKey
}

func (s *stubTopology) GetPeers() []core.PeerInfo     { return s.peers }
func (s *stubTopology) GetTree() []core.TreeEntryInfo { return s.tree }
func (s *stubTopology) Address() net.IP               { return s.ip }
func (s *stubTopology) PublicKey() ed25519.PublicKey  { return s.key }

func mustKey(t *testing.T, hexStr string) ed25519.PublicKey {
	bytes, err := hex.DecodeString(hexStr)
	if err != nil {
		t.Fatalf("failed to decode key: %v", err)
	}
	return ed25519.PublicKey(bytes)
}

func TestHandleTopologyReturnsExpectedPayload(t *testing.T) {
	peerKey := mustKey(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	rootKey := mustKey(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	treeKey := mustKey(t, "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	parentKey := mustKey(t, "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")

	topo := &stubTopology{
		peers: []core.PeerInfo{
			{
				URI:     "tcp://example:1234",
				Up:      true,
				Inbound: false,
				Key:     peerKey,
				Root:    rootKey,
				Port:    7000,
				Coords:  []uint64{1, 2, 3},
			},
		},
		tree: []core.TreeEntryInfo{
			{
				Key:      treeKey,
				Parent:   parentKey,
				Sequence: 42,
			},
		},
		ip:  net.ParseIP("200::1"),
		key: peerKey,
	}

	handler := HandleTopology(topo)
	req := httptest.NewRequest(http.MethodGet, "/topology", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("expected JSON response, got %s", resp.Header.Get("Content-Type"))
	}

	var body TopologyInfo
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if body.OurIPv6 != topo.ip.String() {
		t.Fatalf("expected ipv6 %s, got %s", topo.ip, body.OurIPv6)
	}
	if body.OurPublicKey != hex.EncodeToString(topo.key) {
		t.Fatalf("unexpected public key: %s", body.OurPublicKey)
	}
	if len(body.Peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(body.Peers))
	}
	if body.Peers[0].URI != topo.peers[0].URI {
		t.Fatalf("unexpected peer uri %s", body.Peers[0].URI)
	}
	if body.Peers[0].PublicKey != hex.EncodeToString(peerKey) {
		t.Fatalf("unexpected peer key %s", body.Peers[0].PublicKey)
	}
	if len(body.Tree) != 1 || body.Tree[0].Sequence != 42 {
		t.Fatalf("unexpected tree payload: %+v", body.Tree)
	}
}
