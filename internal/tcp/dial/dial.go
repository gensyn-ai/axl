package dial

import (
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/yggdrasil-network/yggdrasil-go/src/address"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

var (
	ErrInvalidPeerId = errors.New("invalid peer ID")
	ErrDialPeer      = errors.New("failed to reach peer")
)

func DialPeerConnection(netStack *stack.Stack, tcpPort int, peerId string, timeout time.Duration) (*gonet.TCPConn, error) {

	peerIdBytes, err := hex.DecodeString(peerId)
	if err != nil || len(peerIdBytes) != 32 {
		return nil, ErrInvalidPeerId
	}
	var keyArr [32]byte
	copy(keyArr[:], peerIdBytes)
	destAddr := address.AddrForKey(keyArr[:])

	// Dial the remote peer
	destIP := tcpip.AddrFromSlice(destAddr[:])
	conn, err := gonet.DialTCP(netStack, tcpip.FullAddress{
		NIC:  0,
		Addr: destIP,
		Port: uint16(tcpPort),
	}, header.IPv6ProtocolNumber)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDialPeer, err)
	}
	conn.SetReadDeadline(time.Now().Add(timeout))

	return conn, nil
}
