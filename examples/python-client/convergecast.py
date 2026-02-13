"""
Simple convergecast over Yggdrasil network.

Uses client.py for send/recv operations.
Discovers parent from topology and sends aggregated data up the tree.
"""

import time
import msgpack
from dataclasses import dataclass

from client import get_topology, send_msg_via_bridge, recv_msg_via_bridge


@dataclass
class TreePosition:
    """Our position in the spanning tree."""
    our_key: str
    parent: str | None  # None if we are root
    children: set[str]
    is_root: bool
    is_leaf: bool


def derive_tree_position(topology: dict) -> TreePosition:
    """
    Derive our position in the tree from topology info.

    The tree[] array contains entries with {public_key, parent}.
    We find ourselves, identify our parent, and reverse-lookup children.
    """
    our_key = topology["our_public_key"]
    tree_map = {e["public_key"]: e.get("parent") or None for e in topology.get("tree", [])}

    parent = tree_map.get(our_key)
    children = {k for k, p in tree_map.items() if p == our_key}

    return TreePosition(
        our_key=our_key,
        parent=parent,
        children=children,
        is_root=parent is None,
        is_leaf=not children,
    )


def run_convergecast(local_data: dict, session_id: str = "default",
                     timeout: float = 30.0, topology: dict | None = None):
    """
    Simple synchronous convergecast.
    
    1. Get topology and find our position
    2. Wait for children's data (if any)
    3. Merge with our local data
    4. Send to parent (if not root)
    """
    topo = topology or get_topology()
    if not topo:
        print("Failed to get topology")
        return None
    
    tree = derive_tree_position(topo)
    
    print(f"Convergecast starting...")
    print(f"  Our key: {tree.our_key[:16]}...")
    print(f"  Role: {'ROOT' if tree.is_root else 'LEAF' if tree.is_leaf else 'INTERMEDIATE'}")
    print(f"  Parent: {tree.parent[:16] + '...' if tree.parent else 'None'}")
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
            
            try:
                data = msgpack.unpackb(msg['data'], raw=False)
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
    import argparse
    
    parser = argparse.ArgumentParser(description="Convergecast over Yggdrasil network")
    parser.add_argument("--session", default="default", help="Session ID for this convergecast")
    parser.add_argument("--timeout", type=float, default=30.0, help="Timeout in seconds for waiting for children")
    
    args = parser.parse_args()
    
    topo = get_topology()
    if not topo:
        print("Failed to get topology")
        exit(1)

    local_data = {topo["our_public_key"][:8]: 1}
    result = run_convergecast(local_data, args.session, args.timeout, topology=topo)
    
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
