# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Go microservice that automatically forwards funds from a Binance master account to external wallets. It enables sub-accounts (which cannot withdraw externally due to Binance restrictions) to perform withdrawals via an internal transfer → auto-forward pattern.

**Flow:** Sub-account → internal transfer → Master account → this service → External wallet

## Build and Run Commands

```bash
# Build
go build -o binance-withdrawal ./cmd/service

# Run
./binance-withdrawal --config config.yaml

# Run tests
go test ./...

# Run tests for a specific package
go test ./internal/forwarder/...

# Run tests with verbose output
go test -v ./...

# Run a specific test
go test -v -run TestForwarder_Forward ./internal/forwarder/...
```

## Architecture

### Component Pipeline

```
Detector (WebSocket) → Forwarder → Binance API (REST)
                           ↓
                      BoltDB Store
```

- **Detector** (`internal/detector/`): Monitors incoming transfers via Binance User Data Stream WebSocket
- **Forwarder** (`internal/forwarder/`): Processes transfers with two-layer idempotency protection
- **Poller** (`internal/poller/`): Tracks completion status of forwarded withdrawals
- **API Server** (`internal/api/`): HTTP endpoints for monitoring and manual intervention

### Two-Layer Idempotency

Critical design pattern to prevent duplicate withdrawals:
1. **Layer 1 (BoltDB)**: Fast-path local duplicate check using `tranId`
2. **Layer 2 (Binance API)**: Query withdrawal history by `withdrawOrderId` before initiating

The `withdrawOrderId` parameter is set to the incoming transfer's `tranId`, creating a correlation key.

### State Machine

Transfer lifecycle defined in `internal/sm/statemachine.go`:
```
UNKNOWN → PENDING → FORWARDED → COMPLETED
              ↓          ↓
           FAILED ←──────┘
```

### Key Packages

| Package | Purpose |
|---------|---------|
| `internal/model` | Core domain types (`TransferRecord`, `TransferStatus`) |
| `internal/store` | BoltDB persistence with status indexes |
| `internal/binance` | Binance REST client and WebSocket |
| `internal/config` | Destination address mappings |
| `internal/retry` | Exponential backoff for transient errors |
| `internal/metrics` | Prometheus metrics |
| `internal/audit` | Audit logging for withdrawals |

### Configuration

- Copy `config.example.yaml` to `config.yaml`
- API credentials via environment variables: `BINANCE_MASTER_API_KEY`, `BINANCE_MASTER_API_SECRET`
- Destinations are explicitly configured per sub-account and asset

## Issue Tracking

This project uses `bd` (beads) for issue tracking. See `AGENTS.md` for workflow commands.
