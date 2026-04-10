package listen

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/gensyn-ai/axl/api"
	"github.com/gensyn-ai/axl/internal/a2a"
	"github.com/gensyn-ai/axl/internal/mcp"

	"github.com/gologme/log"
	"github.com/yggdrasil-network/yggdrasil-go/src/address"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
	"github.com/yggdrasil-network/yggdrasil-go/src/ipv6rwc"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
)

var (
	NetStack *stack.Stack

	// MaxConcurrentConns limits the number of simultaneous TCP connections from peers.
	MaxConcurrentConns = 128
	// ConnReadTimeout is the deadline for reading a complete length-prefixed message.
	ConnReadTimeout = 60 * time.Second
	// ConnIdleTimeout is how long a connection can sit idle before being closed.
	ConnIdleTimeout = 5 * time.Minute

	// connSem limits concurrent TCP connections. Initialized in SetupNetworkStack.
	connSem chan struct{}
)

func SetupNetworkStack(yggCore *core.Core, tcpPort int, mcpRouterUrl string, a2aURL string) {
	// Initialize connection semaphore with configured limit
	connSem = make(chan struct{}, MaxConcurrentConns)

	// Create ipv6rwc wrapper
	rwc := ipv6rwc.NewReadWriteCloser(yggCore)

	// Create channel endpoint
	// 1280 is min IPv6 MTU
	// Increased buffer size from 1024 to 8192 for better throughput
	ep := channel.New(8192, 1280, "")

	// Pump: Inbound (Ygg -> Stack)
	go func() {
		buf := make([]byte, 65535)
		for {
			n, err := rwc.Read(buf)
			if err != nil {
				log.Printf("RWC Read error: %v", err)
				break
			}
			if n == 0 {
				continue
			}

			// Inject into stack
			// Create packet buffer with data
			view := buffer.NewViewWithData(append([]byte(nil), buf[:n]...))
			pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
				Payload: buffer.MakeWithView(view),
			})
			ep.InjectInbound(header.IPv6ProtocolNumber, pkt)
		}
	}()

	// Pump: Outbound (Stack -> Ygg)
	go func() {
		for {
			pkt := ep.Read()
			if pkt == nil {
				time.Sleep(1 * time.Millisecond) // Poll if empty
				continue
			}

			// Serialize packet to bytes
			// Use AsSlices() to get all data views
			// Pre-allocate buffer to avoid repeated allocations
			slices := pkt.AsSlices()
			totalLen := 0
			for _, v := range slices {
				totalLen += len(v)
			}
			bs := make([]byte, 0, totalLen)
			for _, v := range slices {
				bs = append(bs, v...)
			}

			rwc.Write(bs)
			pkt.DecRef()
		}
	}()

	// Initialize Stack with TCP performance tuning
	NetStack = stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
	})

	// Configure TCP stack options for better performance
	// Increase send/receive buffer sizes (default is often too small)
	NetStack.SetTransportProtocolOption(tcp.ProtocolNumber, &tcpip.TCPSendBufferSizeRangeOption{
		Min:     4096,
		Default: 1024 * 1024,     // 1 MB default
		Max:     8 * 1024 * 1024, // 8 MB max
	})
	NetStack.SetTransportProtocolOption(tcp.ProtocolNumber, &tcpip.TCPReceiveBufferSizeRangeOption{
		Min:     4096,
		Default: 1024 * 1024,     // 1 MB default
		Max:     8 * 1024 * 1024, // 8 MB max
	})

	// Create NIC
	nicID := tcpip.NICID(1)
	if err := NetStack.CreateNIC(nicID, ep); err != nil {
		log.Fatalf("CreateNIC failed: %v", err)
	}

	// Add Protocol Address
	// Yggdrasil Address
	yggAddr := tcpip.AddrFromSlice(yggCore.Address())
	protocolAddr := tcpip.ProtocolAddress{
		Protocol: header.IPv6ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{
			Address:   yggAddr,
			PrefixLen: 64,
		},
	}
	if err := NetStack.AddProtocolAddress(nicID, protocolAddr, stack.AddressProperties{}); err != nil {
		log.Fatalf("AddProtocolAddress failed: %v", err)
	}

	// Add Route
	NetStack.SetRouteTable([]tcpip.Route{
		{
			Destination: header.IPv6EmptySubnet,
			NIC:         nicID,
		},
	})

	// Start TCP Listener
	go startTCPListener(tcpPort, mcpRouterUrl, a2aURL)
}

func startTCPListener(tcpPort int, mcpRouterUrl string, a2aURL string) {
	// Listen on [::]:7000
	listener, err := gonet.ListenTCP(NetStack, tcpip.FullAddress{
		NIC:  0,
		Port: uint16(tcpPort),
	}, header.IPv6ProtocolNumber)

	if err != nil {
		log.Fatalf("ListenTCP failed: %v", err)
	}

	fmt.Printf("TCP Listener started on port %d\n", tcpPort)

	mux := NewMultiplexer()
	if mcpRouterUrl != "" {
		mcpStream := mcp.NewMCPStream(mcpRouterUrl)
		mux.AddSource(mcpStream, func() any { return &api.MCPMessage{} })
	}
	if a2aURL != "" {
		a2aStream := a2a.NewA2AStream(a2aURL)
		mux.AddSource(a2aStream, func() any { return &api.A2AMessage{} })
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}
		select {
		case connSem <- struct{}{}:
			go func() {
				defer func() { <-connSem }()
				handleTCPConn(conn, mux)
			}()
		default:
			log.Printf("Connection limit reached (%d), rejecting connection from %s",
				MaxConcurrentConns, conn.RemoteAddr())
			conn.Close()
		}
	}
}

// peerIDFromAddr derives a hex-encoded Yggdrasil public key from a peer's network address.
func peerIDFromAddr(addr net.Addr) string {
	host, _, _ := net.SplitHostPort(addr.String())
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	var addrBytes [16]byte
	copy(addrBytes[:], ip.To16())
	yggAddr := address.Address(addrBytes)
	key := yggAddr.GetKey()
	return hex.EncodeToString(key)
}

func handleTCPConn(conn net.Conn, mux *Multiplexer) {
	defer conn.Close()

	fromPeerId := peerIDFromAddr(conn.RemoteAddr())
	log.Printf("Connection from peer %s...", fromPeerId[:16])

	// Protocol: Length(4 bytes) + Data
	for {
		// Idle timeout: close if no new message arrives within the window
		conn.SetReadDeadline(time.Now().Add(ConnIdleTimeout))

		// Read Length
		lenBuf := make([]byte, 4)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			if err != io.EOF {
				log.Printf("Read length error: %v", err)
			}
			return
		}
		length := binary.BigEndian.Uint32(lenBuf)
		if length > api.MaxMessageSize {
			log.Printf("Message size %d from peer %s exceeds maximum %d, closing connection",
				length, fromPeerId[:16], api.MaxMessageSize)
			return
		}

		// Message read timeout: once we have the length, the full payload must arrive promptly
		conn.SetReadDeadline(time.Now().Add(ConnReadTimeout))

		// Read Data
		dataBuf := make([]byte, length)
		if _, err := io.ReadFull(conn, dataBuf); err != nil {
			log.Printf("Read data error: %v", err)
			return
		}

		// Use stream multiplexing for server applications (MCP), like HTTP/2
		handled := false
		for _, stream := range mux.sources {
			msgPtr := mux.requestTypes[stream.GetID()]()
			if stream.IsAllowed(dataBuf, msgPtr) {
				respBytes, err := stream.Forward(msgPtr, fromPeerId)
				if err != nil {
					log.Printf("Stream %s forward error: %v", stream.GetID(), err)
				} else if respBytes != nil {
					if err := sendResponse(conn, respBytes); err != nil {
						log.Printf("Stream %s failed to send response: %v", stream.GetID(), err)
					}
				}
				handled = true
				break
			}
		}

		// Only queue for /recv if no stream handled the message
		if !handled {
			api.DefaultRecvQueue.Push(api.ReceivedMessage{
				FromPeerId: fromPeerId,
				Data:       dataBuf,
			})
		}
	}
}

// sendResponse sends a response back to a peer
func sendResponse(conn net.Conn, data []byte) error {
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))

	if _, err := conn.Write(lenBuf); err != nil {
		return fmt.Errorf("failed to write length: %w", err)
	}
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}
	return nil
}
