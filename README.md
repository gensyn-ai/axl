# Yggdrasil Bandwidth Testing Framework

A comprehensive framework for testing bandwidth and latency over the Yggdrasil network using PyTorch tensor transfers.

## Overview

This project provides tools to measure network performance when transferring PyTorch tensors over Yggdrasil's encrypted mesh network. It includes both bridge-based (HTTP) and direct socket implementations.

## Components

### 1. Bridge-based Client (`client.py`)
- Uses HTTP bridge to communicate with Yggdrasil
- Supports topology discovery
- Includes warmup phase for accurate measurements
- Measures bandwidth using round-trip time

### 2. Direct Socket Implementation (`daemon-clients/`)
- **`client.py`**: Direct IPv6 socket client
- **`server.py`**: Direct IPv6 socket server/receiver
- Lower overhead than bridge-based approach
- Includes tensor verification

### 3. Go Bridge (`client/client.go`)
- HTTP bridge for Python-Yggdrasil communication
- Provides REST API for send/recv operations
- Topology information endpoint

## Setup

### Clone with Submodules
```bash
git clone --recurse-submodules <your-repo-url>
```

Or if already cloned:
```bash
git submodule update --init --recursive
```

### Python Dependencies
```bash
python -m venv .venv
source .venv/bin/activate  # On Windows: .venv\Scripts\activate
pip install torch msgpack requests numpy
```

### Go Dependencies
```bash
cd client
go mod download
```

## Usage

### Bridge-based Testing

1. Start the Go bridge:
```bash
cd client
go run client.go
```

2. Run receiver on one node:
```bash
python client.py recv
```

3. Run bandwidth test on another node:
```bash
python client.py bandwidth [target_public_key]
```

### Direct Socket Testing

1. Start server on receiver node:
```bash
python daemon-clients/server.py
```

2. Run client on sender node (update TARGET_IP first):
```bash
python daemon-clients/client.py
```

## Features

- **Warmup Phase**: Initializes connections before measurements
- **Deterministic Tensors**: Verifiable data integrity
- **Multiple Test Sizes**: From 10x10 to 10000x10000 tensors
- **Round-trip Verification**: ACK-based confirmation
- **Bandwidth Metrics**: MB/s measurements with timing breakdown

## Architecture

```
┌─────────────┐         ┌──────────────┐         ┌─────────────┐
│   Python    │ ◄─HTTP─►│  Go Bridge   │◄─Ygg──►│   Python    │
│   Client    │         │              │         │  Receiver   │
└─────────────┘         └──────────────┘         └─────────────┘

        OR (Direct Socket)

┌─────────────┐                                  ┌─────────────┐
│   Python    │◄──────── IPv6/Yggdrasil ───────►│   Python    │
│   Client    │                                  │   Server    │
└─────────────┘                                  └─────────────┘
```

## Submodules

- **yggdrasil-go**: Official Yggdrasil implementation (https://github.com/yggdrasil-network/yggdrasil-go)

## Notes

- Bandwidth calculations use round-trip time for fair comparison
- All tensor transfers are verified using deterministic seeds
- IPv6 addresses are Yggdrasil mesh addresses (200::/7 range)
