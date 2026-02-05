package tcpdial

import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/yggdrasil-network/yggdrasil-go/src/address"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

func DialPeerConnection(netStack *stack.Stack, tcpPort int, peerKeyHex string, timeout time.Duration) (*gonet.TCPConn, error) {

	destKeyBytes, err := hex.DecodeString(peerKeyHex)
	if err != nil || len(destKeyBytes) != 32 {
		return nil, fmt.Errorf("invalid peer key")
	}
	var keyArr [32]byte
	copy(keyArr[:], destKeyBytes)
	destAddr := address.AddrForKey(keyArr[:])

	// Dial the remote peer
	destIP := tcpip.AddrFromSlice(destAddr[:])
	conn, err := gonet.DialTCP(netStack, tcpip.FullAddress{
		NIC:  0,
		Addr: destIP,
		Port: uint16(tcpPort),
	}, header.IPv6ProtocolNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to reach peer: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(timeout))

	return conn, nil
}
