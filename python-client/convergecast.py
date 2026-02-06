"""
Simple convergecast over Yggdrasil network.

Uses client.py for send/recv operations.
Discovers parent from topology and sends aggregated data up the tree.
"""

import time
import base64
import msgpack
from dataclasses import dataclass
from typing import Optional, Dict, Set

from client import get_topology, send_msg_via_bridge, recv_msg_via_bridge


@dataclass 
class TreePosition:
    """Our position in the spanning tree."""
    our_key: str
    parent: Optional[str]  # None if we are root
    children: Set[str]
    is_root: bool = False
    is_leaf: bool = False


def derive_tree_position(topology: dict) -> TreePosition:
    """
    Derive our position in the tree from topology info.
    
    The tree[] array contains entries with {public_key, parent}.
    We find ourselves, identify our parent, and reverse-lookup children.
    """
    our_key = topology["our_public_key"]
    tree_map: Dict[str, Optional[str]] = {}  # key -> parent
    
    for entry in topology.get("tree", []):
        key = entry.get("public_key", "")
        parent = entry.get("parent") or None  # Empty string -> None
        tree_map[key] = parent
    
    # Find our parent
    our_parent = tree_map.get(our_key)
    
    # Find our children (nodes whose parent is us)
    children = {key for key, parent in tree_map.items() if parent == our_key}
    
    is_root = our_parent is None or our_parent == ""
    is_leaf = len(children) == 0
    
    return TreePosition(
        our_key=our_key,
        parent=our_parent if not is_root else None,
        children=children,
        is_root=is_root,
        is_leaf=is_leaf
    )


def run_convergecast(local_data: Dict, session_id: str = "default", timeout: float = 30.0):
    """
    Simple synchronous convergecast.
    
    1. Get topology and find our position
    2. Wait for children's data (if any)
    3. Merge with our local data
    4. Send to parent (if not root)
    """
    # Get topology
    topo = get_topology()
    if not topo:
        print("Failed to get topology")
        return None
    
    tree = derive_tree_position(topo)
    
    print(f"Convergecast starting...")
    print(f"  Our key: {tree.our_key[:16]}...")
    print(f"  Role: {'ROOT' if tree.is_root else 'LEAF' if tree.is_leaf else 'INTERMEDIATE'}")
    print(f"  Parent: {tree.parent[:16] if tree.parent else 'None'}...")
    print(f"  Children: {len(tree.children)}")
    print(f"  Local data: {local_data}")
    print()
    
    # Start with our local data
    aggregated = dict(local_data)
    received_from = set()
    
    # If we have children, wait for their data
    if tree.children:
        print(f"Waiting for {len(tree.children)} children (timeout={timeout}s)...")
        pending = set(tree.children)
        deadline = time.time() + timeout
        
        while pending and time.time() < deadline:
            msg = recv_msg_via_bridge()
            if msg is None:
                time.sleep(0.01)
                continue
            
            # Decode msgpack (data may be base64-encoded string)
            try:
                raw_data = msg['data']
                if isinstance(raw_data, str):
                    raw_data = base64.b64decode(raw_data)
                data = msgpack.unpackb(raw_data, raw=False)
            except Exception as e:
                print(f"  Decode error: {e}")
                continue
            
            # Check if it's convergecast data for our session
            if data.get("type") != "convergecast_data":
                continue
            if data.get("session_id") != session_id:
                continue
            
            from_key = data.get("from", "")
            if from_key in pending:
                print(f"  Received from child: {from_key[:16]}...")
                child_data = data.get("data", {})
                aggregated.update(child_data)
                received_from.add(from_key)
                pending.discard(from_key)
        
        if pending:
            print(f"  Timeout! Missing: {[k[:16] for k in pending]}")
    
    # If not root, send to parent
    if not tree.is_root and tree.parent:
        msg = {
            "type": "convergecast_data",
            "session_id": session_id,
            "from": tree.our_key,
            "data": aggregated
        }
        packed = msgpack.packb(msg, use_bin_type=True)
        
        print(f"Sending to parent: {tree.parent[:16]}...")
        result = send_msg_via_bridge(tree.parent, packed)
        if result:
            print(f"  Sent {result['sent_bytes']} bytes")
        else:
            print("  Send failed!")
    
    # Return result
    missing = tree.children - received_from
    return {
        "success": len(missing) == 0,
        "is_root": tree.is_root,
        "data": aggregated,
        "received_from": received_from,
        "missing": missing
    }


if __name__ == "__main__":
    import sys
    
    if len(sys.argv) > 1 and sys.argv[1] == "topo":
        # Show topology
        topo = get_topology()
        if topo:
            tree = derive_tree_position(topo)
            print(f"Our key: {tree.our_key}")
            print(f"Parent: {tree.parent or 'None (ROOT)'}")
            print(f"Children: {tree.children}")
            print(f"Is root: {tree.is_root}")
            print(f"Is leaf: {tree.is_leaf}")
    else:
        # Run convergecast
        # Parse args for local data
        local_data = {}
        session_id = "default"
        timeout = 30.0
        
        args = sys.argv[1:]
        i = 0
        while i < len(args):
            if args[i] == "--session" and i + 1 < len(args):
                session_id = args[i + 1]
                i += 2
            elif args[i] == "--timeout" and i + 1 < len(args):
                timeout = float(args[i + 1])
                i += 2
            elif "=" in args[i]:
                key, value = args[i].split("=", 1)
                try:
                    value = int(value)
                except ValueError:
                    try:
                        value = float(value)
                    except ValueError:
                        pass
                local_data[key] = value
                i += 1
            else:
                i += 1
        
        # Default local data if none provided
        if not local_data:
            topo = get_topology()
            if topo:
                local_data = {topo["our_public_key"][:8]: 1}
        
        result = run_convergecast(local_data, session_id, timeout)
        
        if result:
            print()
            print("=" * 60)
            print("CONVERGECAST RESULT")
            print("=" * 60)
            print(f"  Success: {result['success']}")
            print(f"  Is Root: {result['is_root']}")
            print(f"  Received from: {len(result['received_from'])} children")
            print(f"  Missing: {result['missing']}")
            print(f"  Aggregated data: {result['data']}")
            print("=" * 60)
