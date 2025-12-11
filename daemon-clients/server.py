import struct
import msgpack
import torch
import numpy as np


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

def verify_tensor(received_tensor, expected_shape, seed=42, tolerance=1e-6):
    """
    Verifies that a received tensor matches the expected deterministic tensor.
    
    Args:
        received_tensor: The tensor received from the network
        expected_shape: Expected shape of the tensor
        seed: Seed used to create the original tensor
        tolerance: Numerical tolerance for floating point comparison
        
    Returns:
        Boolean indicating if verification passed
    """
    # Create the expected tensor
    expected_tensor = create_deterministic_tensor(tuple(expected_shape), seed)
    
    # Check shape
    if list(received_tensor.shape) != expected_shape:
        print(f"Shape mismatch! Expected {expected_shape}, got {list(received_tensor.shape)}")
        return False
    
    # Check values
    if not torch.allclose(received_tensor, expected_tensor, atol=tolerance):
        max_diff = torch.max(torch.abs(received_tensor - expected_tensor)).item()
        print(f"Value mismatch! Max difference: {max_diff}")
        return False
    
    return True


import socket
# ... Include the helper functions above ...

# Listen on all IPv6 interfaces (::)
# or bind specifically to your Yggdrasil IP: '201:....'
HOST = '200:ce36:a4bd:92a2:11c5:f588:8a11:349d'
PORT = 6000

def start_server():
    # Note: AF_INET6 is crucial for Yggdrasil
    with socket.socket(socket.AF_INET6, socket.SOCK_STREAM) as s:
        # Allow reusing the address if you restart quickly
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)

        s.bind((HOST, PORT))
        s.listen()
        print(f"Node listening on port {PORT} via Yggdrasil...")
        print(f"Ready to receive bandwidth test data...\n")

        conn, addr = s.accept()
        with conn:
            print(f"Connected by peer: {addr[0]}")
            print("="*60)
            
            while True:
                # Use our helper to receive a full message
                data = recv_msg(conn)

                if data is None:
                    print("\nPeer disconnected.")
                    break

                # Handle warmup messages
                if data.get("type") == "warmup":
                    print("Received warmup message")
                    # Send simple ACK for warmup
                    warmup_ack = {
                        "type": "warmup_ack"
                    }
                    send_msg(conn, warmup_ack)
                    print("Warmup ACK sent")
                    print("-" * 60)
                
                # Handle bandwidth test messages
                elif data.get("type") == "bandwidth_test":
                    test_name = data.get("test_name", "Unknown")
                    shape = data.get("shape")
                    seed = data.get("seed", 42)
                    
                    print(f"\nReceiving: {test_name}")
                    print(f"Shape: {shape}")
                    
                    # Deserialize the tensor
                    received_tensor = deserialize_tensor(data["tensor"])
                    
                    # Verify the tensor
                    verified = verify_tensor(received_tensor, shape, seed)
                    
                    if verified:
                        print(f"✓ Verification PASSED")
                    else:
                        print(f"✗ Verification FAILED")
                    
                    # Send acknowledgment
                    ack = {
                        "type": "ack",
                        "test_name": test_name,
                        "verified": verified
                    }
                    send_msg(conn, ack)
                    print("-" * 60)
                
                # Handle other message types
                elif data.get("type") == "sensor_reading":
                    print(f"Temperature processed: {data['value']}C")

if __name__ == "__main__":
    try:
        start_server()
    except KeyboardInterrupt:
        print("\n\nServer stopped by user.")
    except Exception as e:
        print(f"\nServer error: {e}")
        import traceback
        traceback.print_exc()
