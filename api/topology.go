package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)

// TopologyInfo returned by /topology
type TopologyInfo struct {
	OurIPv6      string     `json:"our_ipv6"`
	OurPublicKey string     `json:"our_public_key"`
	Peers        []PeerInfo `json:"peers"`
	Tree         []TreeInfo `json:"tree"`
}

type PeerInfo struct {
	URI       string   `json:"uri"`
	Up        bool     `json:"up"`
	Inbound   bool     `json:"inbound"`
	PublicKey string   `json:"public_key"`
	Root      string   `json:"root"`
	Port      uint64   `json:"port"`
	Coords    []uint64 `json:"coords"`
}

type TreeInfo struct {
	PublicKey string `json:"public_key"`
	Parent    string `json:"parent"`
	Sequence  uint64 `json:"sequence"`
}

var yggCore *core.Core

func HandleTopology(yggCore *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		peers := yggCore.GetPeers()
		tree := yggCore.GetTree()

		var peerInfos []PeerInfo
		for _, p := range peers {
			peerInfos = append(peerInfos, PeerInfo{
				URI:       p.URI,
				Up:        p.Up,
				Inbound:   p.Inbound,
				PublicKey: hex.EncodeToString(p.Key),
				Root:      hex.EncodeToString(p.Root),
				Port:      p.Port,
				Coords:    p.Coords,
			})
		}

		var treeInfos []TreeInfo
		for _, t := range tree {
			treeInfos = append(treeInfos, TreeInfo{
				PublicKey: hex.EncodeToString(t.Key),
				Parent:    hex.EncodeToString(t.Parent),
				Sequence:  t.Sequence,
			})
		}

		info := TopologyInfo{
			OurIPv6:      yggCore.Address().String(),
			OurPublicKey: hex.EncodeToString(yggCore.PublicKey()),
			Peers:        peerInfos,
			Tree:         treeInfos,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(info)
	}
}
