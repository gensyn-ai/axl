package main

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gologme/log"
	"github.com/yggdrasil-network/yggdrasil-go/src/address"
	"github.com/yggdrasil-network/yggdrasil-go/src/config"
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
	yggCore   *core.Core
	netStack  *stack.Stack
	recvMutex sync.Mutex
	recvQueue []ReceivedMessage
)

// ReceivedMessage holds incoming data with sender info
type ReceivedMessage struct {
	FromKey string `json:"from_key"`
	Data    []byte `json:"data"`
}

// SendRequest is what Python sends to /send
type SendRequest struct {
	DestinationKey string `json:"destination_key"` // Hex-encoded public key
	Data           []byte `json:"data"`
}

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

const TCPPort = 7000

func main() {
	// Command-line flags
	listenAddr := flag.String("listen", "", "Listen address for incoming peers (e.g., tls://0.0.0.0:9001). If set, runs as a server.")
	peerAddr := flag.String("peer", "", "Peer address to connect to (e.g., tls://1.2.3.4:9001). If not set and not listening, uses default public peer.")
	flag.Parse()

	// 1. Generate config
	cfg := config.GenerateConfig()
	cfg.IfName = "none"

	// Create logger
	logger := log.New(os.Stdout, "[ygg] ", 0)
	logger.EnableLevel("info")
	logger.EnableLevel("warn")
	logger.EnableLevel("error")

	// 2. Start the Yggdrasil core
	var err error
	options := []core.SetupOption{}

	if *listenAddr != "" {
		fmt.Printf("Will listen on: %s\n", *listenAddr)
		options = append(options, core.ListenAddress(*listenAddr))
	}

	if *peerAddr != "" {
		fmt.Printf("Will connect to peer: %s\n", *peerAddr)
		options = append(options, core.Peer{URI: *peerAddr})
	} else if *listenAddr == "" {
		defaultPeer := "tls://34.173.99.229:9001"
		fmt.Printf("Using default peer: %s\n", defaultPeer)
		options = append(options, core.Peer{URI: defaultPeer})
	}

	yggCore, err = core.New(cfg.Certificate, logger, options...)
	if err != nil {
		log.Fatalf("Failed to start Yggdrasil core: %v", err)
	}
	defer yggCore.Stop()

	fmt.Println("Yggdrasil Userspace Node Started!")
	fmt.Printf("Our IPv6: %s\n", yggCore.Address().String())
	fmt.Printf("Our Public Key: %s\n", hex.EncodeToString(yggCore.PublicKey()))

	// 3. Setup Userspace Network Stack (gVisor)
	setupNetworkStack()

	// 4. Start HTTP bridge for Python
	http.HandleFunc("/topology", handleTopology)
	http.HandleFunc("/send", handleSend)
	http.HandleFunc("/recv", handleRecv)

	fmt.Println("Starting Local HTTP Bridge on localhost:9002...")
	if err := http.ListenAndServe("127.0.0.1:9002", nil); err != nil {
		log.Fatalf("HTTP Server failed: %v", err)
	}
}

func setupNetworkStack() {
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
	netStack = stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
	})

	// Configure TCP stack options for better performance
	// Increase send/receive buffer sizes (default is often too small)
	netStack.SetTransportProtocolOption(tcp.ProtocolNumber, &tcpip.TCPSendBufferSizeRangeOption{
		Min:     4096,
		Default: 1024 * 1024,     // 1 MB default
		Max:     8 * 1024 * 1024, // 8 MB max
	})
	netStack.SetTransportProtocolOption(tcp.ProtocolNumber, &tcpip.TCPReceiveBufferSizeRangeOption{
		Min:     4096,
		Default: 1024 * 1024,     // 1 MB default
		Max:     8 * 1024 * 1024, // 8 MB max
	})

	// Create NIC
	nicID := tcpip.NICID(1)
	if err := netStack.CreateNIC(nicID, ep); err != nil {
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
	if err := netStack.AddProtocolAddress(nicID, protocolAddr, stack.AddressProperties{}); err != nil {
		log.Fatalf("AddProtocolAddress failed: %v", err)
	}

	// Add Route
	netStack.SetRouteTable([]tcpip.Route{
		{
			Destination: header.IPv6EmptySubnet,
			NIC:         nicID,
		},
	})

	// Start TCP Listener
	go startTCPListener()
}

func startTCPListener() {
	// Listen on [::]:7000
	listener, err := gonet.ListenTCP(netStack, tcpip.FullAddress{
		NIC:  0,
		Port: uint16(TCPPort),
	}, header.IPv6ProtocolNumber)

	if err != nil {
		log.Fatalf("ListenTCP failed: %v", err)
	}

	fmt.Printf("TCP Listener started on port %d\n", TCPPort)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}
		go handleTCPConn(conn)
	}
}

func handleTCPConn(conn net.Conn) {
	defer conn.Close()

	// Protocol: Length(4 bytes) + Data
	for {
		// Read Length
		lenBuf := make([]byte, 4)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			if err != io.EOF {
				log.Printf("Read length error: %v", err)
			}
			return
		}
		length := binary.BigEndian.Uint32(lenBuf)

		// Read Data
		dataBuf := make([]byte, length)
		if _, err := io.ReadFull(conn, dataBuf); err != nil {
			log.Printf("Read data error: %v", err)
			return
		}

		// Identify Sender
		// RemoteAddr is [IPv6]:Port
		remoteAddrStr := conn.RemoteAddr().String()
		host, _, _ := net.SplitHostPort(remoteAddrStr)

		// Convert Host IPv6 -> PublicKey
		fromKey := ""
		ip := net.ParseIP(host)
		if ip != nil {
			var addrBytes [16]byte
			copy(addrBytes[:], ip.To16())
			yggAddr := address.Address(addrBytes)
			key := yggAddr.GetKey()
			fromKey = hex.EncodeToString(key)
		}

		msg := ReceivedMessage{
			FromKey: fromKey,
			Data:    dataBuf,
		}

		recvMutex.Lock()
		if len(recvQueue) >= 100 {
			recvQueue = recvQueue[1:]
		}
		recvQueue = append(recvQueue, msg)
		recvMutex.Unlock()
	}
}

func handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get destination key from header (raw binary, no JSON/base64)
	destKeyHex := r.Header.Get("X-Destination-Key")
	if destKeyHex == "" {
		http.Error(w, "Missing X-Destination-Key header", http.StatusBadRequest)
		return
	}

	// Decode destination public key
	destKeyBytes, err := hex.DecodeString(destKeyHex)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid destination key: %v", err), http.StatusBadRequest)
		return
	}

	// Convert Key -> IPv6 Address
	if len(destKeyBytes) != 32 {
		http.Error(w, "Invalid key length", http.StatusBadRequest)
		return
	}
	var keyArr [32]byte
	copy(keyArr[:], destKeyBytes)
	destAddr := address.AddrForKey(keyArr[:])

	// Read raw binary body directly (no JSON/base64 decoding)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read body: %v", err), http.StatusBadRequest)
		return
	}

	// Dial via gVisor
	destIP := tcpip.AddrFromSlice(destAddr[:])

	conn, err := gonet.DialTCP(netStack, tcpip.FullAddress{
		NIC:  0,
		Addr: destIP,
		Port: uint16(TCPPort),
	}, header.IPv6ProtocolNumber)

	if err != nil {
		http.Error(w, fmt.Sprintf("DialTCP failed: %v", err), http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	// Write Length Prefix
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))

	if _, err := conn.Write(lenBuf); err != nil {
		http.Error(w, fmt.Sprintf("Write length failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Write Data
	if _, err := conn.Write(data); err != nil {
		http.Error(w, fmt.Sprintf("Write data failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Return minimal response
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Sent-Bytes", fmt.Sprintf("%d", len(data)))
	w.WriteHeader(http.StatusOK)
}

func handleRecv(w http.ResponseWriter, r *http.Request) {
	recvMutex.Lock()
	defer recvMutex.Unlock()

	if len(recvQueue) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Pop first message
	msg := recvQueue[0]
	recvQueue = recvQueue[1:]

	// Return raw binary with sender key in header (no JSON/base64)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-From-Key", msg.FromKey)
	w.Write(msg.Data)
}

func handleTopology(w http.ResponseWriter, r *http.Request) {
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
