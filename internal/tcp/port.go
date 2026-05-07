package tcp

// PeerTCPPort is the network-wide overlay TCP port that every AXL node
// listens on inside its gVisor netstack. It is intentionally not
// per-node configurable: each node's gVisor stack is bound to its own
// Yggdrasil IPv6 address and is isolated from the host kernel, so there
// is no collision risk between nodes (or with unrelated host processes).
// Keeping the port uniform across the network lets a sender dial any
// peer using only its peer ID, without needing to discover a port.
const PeerTCPPort = 7000
