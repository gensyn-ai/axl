import struct
import msgpack
import torch
import numpy as np
import json
import socket
import time



# def get_yggdrasil_neighbors():
#     # Connect to the Yggdrasil Admin Socket (Not the public port!)
#     # On Linux, this is often a Unix socket, but can be TCP localhost:9001
#     # Check your /etc/yggdrasil.conf under 'AdminListen'
    
#     # Example using TCP localhost (Enable AdminListen in config first!)
#     try:
#         with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
#             s.connect(('127.0.0.1', 9001))
            
#             # Send the request
#             request = {"request": "getPeers"}
#             s.sendall(json.dumps(request).encode('utf-8'))
            
#             # Read response
#             data = s.recv(16384) # 16KB buffer should be enough for local peers
#             response = json.loads(data.decode('utf-8'))
            
#             print(f"DEBUG: Yggdrasil response status: {response.get('status')}")
            
#             if response['status'] != 'success':
#                 print(f"DEBUG: Error from Yggdrasil: {response.get('error', 'Unknown error')}")
#                 return None

#             peers = response['response']['peers']
#             print(f"DEBUG: Found {len(peers)} peer(s)")
#             return process_peers(peers)
#     except Exception as e:
#         print(f"Could not talk to Yggdrasil daemon: {e}")
#         print(f"Make sure AdminListen is set to 'tcp://localhost:9001' in /etc/yggdrasil.conf")
#         return None

# def process_peers(peers_dict):
#     """
#     Categorizes neighbors into Parents and Children based on coordinates.
#     Peers_dict is a dictionary where keys are IP addresses and values are peer info.
#     """
#     topology = {"parents": [], "children": [], "siblings": []}
    
#     # Yggdrasil returns peers as {"ip_address": {peer_info}, ...}
#     for ip_address, peer_info in peers_dict.items():
#         # 'port' indicates the spanning tree port. 
#         # If port is 0, they are likely your parent (upstream).
#         # If port > 0, they are your children (downstream).
#         port = peer_info.get('port', -1)
        
#         if port == 0:
#             topology['parents'].append(ip_address)
#         else:
#             topology['children'].append(ip_address)
            
#     return topology


def send_msg(sock, data):
    """
    Serializes data with msgpack, prefixes it with the length, 
    and sends it over the socket.
    """
    # 1. Serialize the data to binary bytes
    packed_data = msgpack.packb(data, use_bin_type=True)
    
    # 2. Calculate length (Network Byte Order, 4-byte unsigned integer)
    # '>I' means Big Endian, Unsigned Int
    length_prefix = struct.pack('>I', len(packed_data))
    
    # 3. Send length + data
    sock.sendall(length_prefix + packed_data)

def recv_msg(sock):
    """
    Reads 4 bytes to get length, then reads the payload, 
    and deserializes with msgpack.
    """
    # 1. Read the first 4 bytes (Length Prefix)
    raw_msglen = recvall(sock, 4)
    if not raw_msglen:
        return None
        
    # Unpack the 4 bytes to get an integer size
    msglen = struct.unpack('>I', raw_msglen)[0]
    
    # 2. Read the actual data based on that size
    raw_data = recvall(sock, msglen)
    if not raw_data:
        return None
        
    # 3. Deserialize
    return msgpack.unpackb(raw_data, raw=False)

def recvall(sock, n):
    """Helper function to guarantee we receive exactly n bytes."""
    data = bytearray()
    while len(data) < n:
        packet = sock.recv(n - len(data))
        if not packet:
            return None
        data.extend(packet)
    return data

def serialize_tensor(tensor):
    """
    Serializes a PyTorch tensor into a msgpack-compatible dictionary.
    Stores the tensor data as bytes along with shape and dtype metadata.
    """
    # Convert to numpy array (moves to CPU if needed)
    np_array = tensor.cpu().detach().numpy()
    
    return {
        "tensor_data": np_array.tobytes(),
        "shape": list(np_array.shape),
        "dtype": str(np_array.dtype)
    }

def deserialize_tensor(tensor_dict):
    """
    Reconstructs a PyTorch tensor from serialized data.
    
    Args:
        tensor_dict: Dictionary containing 'tensor_data', 'shape', and 'dtype' keys
        
    Returns:
        PyTorch tensor reconstructed from the serialized data
    """
    dtype = np.dtype(tensor_dict["dtype"])
    np_array = np.frombuffer(tensor_dict["tensor_data"], dtype=dtype)
    np_array = np_array.reshape(tensor_dict["shape"])
    return torch.from_numpy(np_array)

def create_deterministic_tensor(shape, seed=42):
    """
    Creates a deterministic tensor with the given shape and seed.
    Both client and server can use this to create identical tensors.
    
    Args:
        shape: Tuple of dimensions for the tensor
        seed: Random seed for reproducibility
        
    Returns:
        PyTorch tensor initialized with deterministic values
    """
    torch.manual_seed(seed)
    return torch.randn(*shape)


# ... Include the helper functions above ...

# The Yggdrasil IP of the Receiver Node
# REPLACE THIS with the actual address starting with 200... or 300...
TARGET_IP = '200:ce36:a4bd:92a2:11c5:f588:8a11:349d' 
PORT = 6000

def run_bandwidth_test(sock, shape, seed=42, test_name=""):
    """
    Runs a single bandwidth/latency test by sending a tensor of given shape.
    
    Args:
        sock: Connected socket
        shape: Tuple defining tensor dimensions
        seed: Seed for deterministic tensor creation
        test_name: Name/description of this test
        
    Returns:
        Dictionary with test results
    """
    # Create deterministic tensor
    tensor = create_deterministic_tensor(shape, seed)
    tensor_size_bytes = tensor.nelement() * tensor.element_size()
    tensor_size_mb = tensor_size_bytes / (1024 * 1024)
    
    print(f"\n{'='*60}")
    print(f"Test: {test_name}")
    print(f"Shape: {shape}, Size: {tensor_size_mb:.2f} MB")
    
    # Prepare message
    tensor_message = {
        "type": "bandwidth_test",
        "test_name": test_name,
        "shape": list(shape),
        "seed": seed,
        "tensor": serialize_tensor(tensor)
    }
    
    # Measure send time (just buffer copy)
    send_start = time.time()
    send_msg(sock, tensor_message)
    send_time = time.time() - send_start
    
    # Wait for acknowledgment
    ack = recv_msg(sock)
    total_time = time.time() - send_start
    
    # Calculate metrics - use round-trip time for fair comparison
    # (send_time only measures buffer copy, not actual network transmission)
    bandwidth_mbps = (tensor_size_mb / total_time) if total_time > 0 else 0
    
    results = {
        "shape": shape,
        "size_mb": tensor_size_mb,
        "send_time": send_time,
        "total_time": total_time,
        "bandwidth_mbps": bandwidth_mbps,
        "verified": ack.get("verified", False) if ack else False
    }
    
    print(f"Send Time: {send_time*1000:.2f} ms")
    print(f"Total Round-trip: {total_time*1000:.2f} ms")
    print(f"Bandwidth: {bandwidth_mbps:.2f} MB/s")
    print(f"Verified: {'✓' if results['verified'] else '✗'}")
    print(f"{'='*60}")
    
    return results

def start_client():

    # Create IPv6 Socket
    with socket.socket(socket.AF_INET6, socket.SOCK_STREAM) as s:
        s.connect((TARGET_IP, PORT))
        print("Connected!")

        # Warmup phase - send a small tensor to initialize connections
        print("\nWarming up connection...")
        warmup_tensor = create_deterministic_tensor((5, 5), seed=1)
        warmup_msg = {
            "type": "warmup",
            "tensor": serialize_tensor(warmup_tensor)
        }
        send_msg(s, warmup_msg)
        # Wait for warmup ACK
        warmup_ack = recv_msg(s)
        print("Warmup complete.\n")

        # Run bandwidth tests with increasing tensor sizes
        test_configs = [
            ((10, 10), "Small: 10x10"),
            ((100, 100), "Medium: 100x100"),
            ((1000, 1000), "Large: 1000x1000"),
            ((10000, 10000), "XLarge: 10000x10000"),
        ]
        
        results = []
        for shape, test_name in test_configs:
            result = run_bandwidth_test(s, shape, seed=42, test_name=test_name)
            results.append(result)
            time.sleep(0.5)  # Brief pause between tests
        
        # Print summary
        print("\n" + "="*60)
        print("BANDWIDTH TEST SUMMARY")
        print("="*60)
        for r in results:
            print(f"{str(r['shape']):20s} | {r['size_mb']:8.2f} MB | "
                  f"{r['total_time']*1000:8.2f} ms | "
                  f"{r['bandwidth_mbps']:8.2f} MB/s | "
                  f"{'✓' if r['verified'] else '✗'}")
        print("="*60)

if __name__ == "__main__":
    start_client()






