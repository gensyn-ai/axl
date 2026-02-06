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
	ErrInvalidPeerKey = errors.New("invalid peer key")
	ErrDialPeer       = errors.New("failed to reach peer")
)

func DialPeerConnection(netStack *stack.Stack, tcpPort int, peerKeyHex string, timeout time.Duration) (*gonet.TCPConn, error) {

	destKeyBytes, err := hex.DecodeString(peerKeyHex)
	if err != nil || len(destKeyBytes) != 32 {
		return nil, ErrInvalidPeerKey
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
		return nil, fmt.Errorf("%w: %v", ErrDialPeer, err)
	}
	conn.SetReadDeadline(time.Now().Add(timeout))

	return conn, nil
}
