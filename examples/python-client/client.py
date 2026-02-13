"""
This is a simple Python client that demonstrates how to send and recv pytorch tensors over the Yggdrasil mesh network.
It may be run in three modes:
- recv: receive messages and print them
- tensor: send a pytorch tensor to a target node
- bandwidth: send a pytorch tensor to a target node and measure the bandwidth
if running tensor or bandwidth demo, a target peerId must be provided as second arg, and the target node must be running in recv mode.
"""
import time
import requests
import torch
import msgpack
import io

# Configuration
BRIDGE_URL = "http://127.0.0.1:9002"

# Target can be specified as peerId (hex) - get from topology
TARGET_KEY = None  # Will be set from topology or manually


def get_topology():
    """
    Fetch topology from the Go bridge.
    Returns dict with: our_ipv6, our_public_key, peers[], tree[]
    """
    try:
        resp = requests.get(f"{BRIDGE_URL}/topology")
        if resp.status_code == 200:
            return resp.json()
    except Exception as e:
        print(f"Topology fetch failed: {e}")
    return None


def recv_msg_via_bridge():
    """
    Poll for received messages from the Go bridge.
    Returns dict with: from_peer_id, data (raw bytes)
    """
    try:
        resp = requests.get(f"{BRIDGE_URL}/recv")
        if resp.status_code == 200:
            # Raw binary response with sender peer ID in header
            return {
                'from_peer_id': resp.headers.get('X-From-Peer-Id', ''),
                'data': resp.content  # Raw bytes
            }
        elif resp.status_code == 204:
            return None  # No messages
    except Exception as e:
        print(f"Recv failed: {e}")
    return None


def send_msg_via_bridge(dest_key, data):
    """
    Send data to a destination identified by peerId (hex string).
    Data is raw bytes, sent directly without base64 encoding.
    """
    headers = {
        'X-Destination-Peer-Id': dest_key,
        'Content-Type': 'application/octet-stream'
    }
    
    try:
        resp = requests.post(f"{BRIDGE_URL}/send", data=data, headers=headers)
        if resp.status_code == 200:
            sent_bytes = resp.headers.get('X-Sent-Bytes', '0')
            return {'sent_bytes': int(sent_bytes), 'success': True}
        else:
            print(f"Send error: {resp.status_code} - {resp.text}")
            return None
    except Exception as e:
        print(f"Send failed: {e}")
        return None

# ... Tensor helpers ...

def serialize_tensor(tensor):
    buffer = io.BytesIO()
    torch.save(tensor, buffer)
    packet_data = buffer.getvalue()
    return {
        "tensor_data": packet_data,
        "shape": list(tensor.shape),
        "dtype": str(tensor.dtype)
    }

def deserialize_tensor(tensor_dict):
    # dtype = np.dtype(tensor_dict["dtype"])
    bytes_io = io.BytesIO(tensor_dict["tensor_data"])
    tensor = torch.load(bytes_io)
    return tensor

def create_deterministic_tensor(shape, seed=42):
    torch.manual_seed(seed)
    return torch.randn(*shape)

# ... Main Logic ...

def print_topology():
    """Print current network topology."""
    topo = get_topology()
    if not topo:
        print("Failed to get topology")
        return None
    
    print("\n" + "="*60)
    print("YGGDRASIL NODE TOPOLOGY")
    print("="*60)
    print(f"Our IPv6:      {topo['our_ipv6']}")
    print(f"Our peerId: {topo['our_public_key']}")
    
    print(f"\nPeers ({len(topo.get('peers', []))}):")
    for p in topo.get('peers', []):
        status = "UP" if p.get('up') else "DOWN"
        direction = "inbound" if p.get('inbound') else "outbound"
        print(f"  [{status}] {p.get('uri', 'N/A')} ({direction})")
        if p.get('public_key'):
            print(f"       Key: {p['public_key'][:16]}...")
    
    print(f"\nSpanning Tree ({len(topo.get('tree', []))}):")
    for t in topo.get('tree', []):
        print(f"  Node: {t['public_key'][:16]}...")
        print(f"    Parent: {t['parent'][:16] if t['parent'] else 'ROOT'}...")
    
    print("="*60 + "\n")
    return topo

def run_tensor_test(target_key=None):
    """
    Send a 3x3 tensor using torch.arange(9).reshape(3,3).
    """
    print("=== TENSOR TEST ===")
    topo = print_topology()
    
    if not topo:
        return
    
    # Find target
    if target_key is None:
        peers = topo.get('peers', [])
        up_peers = [p for p in peers if p.get('up') and p.get('public_key')]
        if up_peers:
            target_key = up_peers[0]['public_key']
            print(f"Using first connected peer: {target_key[:16]}...")
        else:
            print("No connected peers. Run with a target peerId.")
            return
    
    # Create the tensor
    tensor = torch.arange(9).reshape(3, 3).float()
    print(f"\nSending tensor:\n{tensor}")
    
    # Serialize
    tensor_data = serialize_tensor(tensor)
    
    msg = {
        "type": "tensor_test",
        "from": topo['our_public_key'],
        "timestamp": time.time(),
        "tensor": tensor_data
    }
    
    packed = msgpack.packb(msg, use_bin_type=True)
    print(f"\nPacked size: {len(packed)} bytes")
    
    result = send_msg_via_bridge(target_key, packed)
    if result:
        print(f"Send result: {result}")
    else:
        print("Send failed")


def run_bandwidth_test(target_key=None):
    """
    Run a bandwidth test sequence with increasing tensor sizes.
    """
    print("=== BANDWIDTH TEST ===")
    topo = print_topology()
    
    if not topo:
        return
    
    # Find target
    if target_key is None:
        peers = topo.get('peers', [])
        up_peers = [p for p in peers if p.get('up') and p.get('public_key')]
        if up_peers:
            target_key = up_peers[0]['public_key']
            print(f"Using first connected peer: {target_key[:16]}...")
        else:
            print("No connected peers. Run with a target peerId.")
            return

    # Warmup phase - send a small tensor to initialize connections
    print("\nWarming up connection...")
    warmup_tensor = create_deterministic_tensor((5, 5), seed=1)
    warmup_msg = {
        "type": "warmup",
        "tensor": serialize_tensor(warmup_tensor),
        "timestamp": time.time()
    }
    warmup_packed = msgpack.packb(warmup_msg, use_bin_type=True)
    send_msg_via_bridge(target_key, warmup_packed)
    time.sleep(0.5)  # Give it a moment to process
    print("Warmup complete.\n")

    test_configs = [
        ((10, 10), "Small: 10x10"),
        ((100, 100), "Medium: 100x100"),
        ((1000, 1000), "Large: 1000x1000"),
        ((10000, 10000), "XLarge: 10000x10000"), # Start small for now
    ]
    
    results = []
    
    for shape, test_name in test_configs:
        # Create tensor
        tensor = create_deterministic_tensor(shape, seed=42)
        tensor_size_bytes = tensor.nelement() * tensor.element_size()
        tensor_size_mb = tensor_size_bytes / (1024 * 1024)
        
        print(f"\nTest: {test_name}")
        print(f"Shape: {shape}, Size: {tensor_size_mb:.2f} MB")
        
        msg = {
            "type": "bandwidth_test",
            "test_name": test_name,
            "shape": list(shape),
            "seed": 42,
            "tensor": serialize_tensor(tensor),
            "timestamp": time.time()
        }
        
        packed = msgpack.packb(msg, use_bin_type=True)
        
        print(f"Sending {len(packed)} bytes...")
        
        # Measure only send time for bandwidth calculation
        send_start = time.time()
        if not send_msg_via_bridge(target_key, packed):
            print("Send failed!")
            continue
        send_time = time.time() - send_start
            
        # Wait for echo/ack (for verification only)
        print("Waiting for ACK...")
        ack_received = False
        ack_start = time.time()
        
        while time.time() - ack_start < 2400: # 40min timeout
            resp = recv_msg_via_bridge()
            if resp:
                data = resp['data']  # Already raw bytes
                try:
                    decoded = msgpack.unpackb(data, raw=False)
                    if decoded.get('type') == 'bandwidth_ack':
                        ack_received = True
                        print("ACK Received!")
                        break
                except:
                    pass
            time.sleep(0.1)
            
        total_time = time.time() - send_start
        bandwidth_mbps = (tensor_size_mb / total_time) if total_time > 0 else 0
        
        r = {
            "shape": shape,
            "size_mb": tensor_size_mb,
            "send_time": send_time,
            "total_time": total_time,
            "bandwidth_mbps": bandwidth_mbps,
            "verified": ack_received
        }
        results.append(r)
        
        print(f"Send Time: {send_time*1000:.2f} ms")
        print(f"Total Round-trip: {total_time*1000:.2f} ms")
        print(f"Bandwidth: {bandwidth_mbps:.2f} MB/s")
        print(f"Verified: {'✓' if ack_received else '✗'}")
        
        time.sleep(0.5)

    print("\n" + "="*60)
    print("BANDWIDTH TEST SUMMARY")
    print("="*60)
    for r in results:
        print(f"{str(r['shape']):20s} | {r['size_mb']:8.2f} MB | "
              f"{r['total_time']*1000:8.2f} ms | "
              f"{r['bandwidth_mbps']:8.2f} MB/s | "
              f"{'✓' if r['verified'] else '✗'}")
    print("="*60)


def run_receiver():
    """
    Run as a receiver - print topology and poll for messages.
    """
    print_topology()
    print("Listening for incoming messages (Ctrl+C to stop)...")
    
    try:
        while True:
            msg = recv_msg_via_bridge()
            if msg:
                print(f"\n[RECV] From: {msg.get('from_peer_id', 'unknown')[:32]}...")
                data = msg['data'] if msg.get('data') else b''
                try:
                    decoded = msgpack.unpackb(data, raw=False)
                    msg_type = decoded.get('type', 'unknown')
                    print(f"       Type: {msg_type}")
                    
                    # If it's a tensor, deserialize and display it
                    if 'tensor' in decoded:
                        tensor = deserialize_tensor(decoded['tensor'])
                        print(f"       Tensor Shape: {tensor.shape}")
                        
                        # If it's a bandwidth test, verify and ACK
                        if msg_type == 'bandwidth_test':
                            # Verify deterministic tensor
                            expected = create_deterministic_tensor(tuple(decoded['shape']), decoded['seed'])
                            is_correct = torch.allclose(tensor, expected)
                            print(f"       Verification: {'PASS' if is_correct else 'FAIL'}")
                            
                            # Send ACK
                            ack_msg = {
                                "type": "bandwidth_ack",
                                "verified": is_correct,
                                "timestamp": time.time()
                            }
                            print(f"       Sending ACK to {msg.get('from_peer_id')[:16]}...")
                            send_msg_via_bridge(msg.get('from_peer_id'), msgpack.packb(ack_msg, use_bin_type=True))
                            
                    elif msg_type == 'bandwidth_ack':
                        print(f"       ACK Received (Verified: {decoded.get('verified')})")
                    else:
                        print(f"       Data: {decoded}")
                except Exception as e:
                    print(f"       Raw: {data[:100]}... (error: {e})")
            time.sleep(0.01) # Faster polling for bandwidth test
    except KeyboardInterrupt:
        print("\nStopped.")


if __name__ == "__main__":
    import argparse
    
    parser = argparse.ArgumentParser(description="Yggdrasil Python Client")
    subparsers = parser.add_subparsers(dest="command", help="Available commands")
    
    # recv command
    subparsers.add_parser("recv", help="Receive messages and print them")
    
    # tensor command
    tensor_parser = subparsers.add_parser("tensor", help="Send a pytorch tensor to a target node")
    tensor_parser.add_argument("target", nargs="?", help="Target peerId (optional, defaults to first connected peer)")
    
    # bandwidth command
    bw_parser = subparsers.add_parser("bandwidth", help="Send a pytorch tensor and measure bandwidth")
    bw_parser.add_argument("target", nargs="?", help="Target peerId (optional, defaults to first connected peer)")
    
    args = parser.parse_args()
    
    if args.command == "recv":
        run_receiver()
    elif args.command == "tensor":
        run_tensor_test(args.target)
    elif args.command == "bandwidth":
        run_bandwidth_test(args.target)
    else:
        parser.print_help()
