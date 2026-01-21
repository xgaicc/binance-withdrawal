# Binance Master Withdrawal Service

## Overview

A microservice that automatically forwards funds from a Binance master account to configured external wallets. This service enables sub-accounts (which cannot withdraw externally) to effectively perform withdrawals by transferring funds to the master account, which then forwards them to the intended destination.

## Problem Statement

Binance sub-accounts have a platform restriction: they cannot withdraw funds to external wallets. Only the master account has withdrawal privileges. However, trading systems like Midas operate with sub-account credentials for security isolation.

The rebalancing system requires the ability to move funds from Binance to external wallets (e.g., Solana, Ethereum). Without master account access, this is impossible.

## Solution

### Internal Transfer → Auto-Forward Pattern

Instead of attempting direct external withdrawals from sub-accounts, we split the operation:

1. **Sub-account (Midas)** performs an internal transfer to the master account
   - Uses `POST /sapi/v1/sub-account/transfer/subToMaster`
   - Instant, free (no blockchain fees)
   - Requires only internal transfer permissions on sub-account API key

2. **Master Withdrawal Service** detects incoming transfers and forwards them
   - Polls for incoming sub-account transfers
   - Looks up configured destination for the asset/sub-account
   - Initiates external withdrawal with master credentials

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           BINANCE PLATFORM                                  │
│                                                                             │
│  ┌─────────────────┐    Internal Transfer    ┌─────────────────┐           │
│  │  Sub-Account    │ ──────────────────────▶ │  Master Account │           │
│  │  (Midas)        │    (instant, free)      │                 │           │
│  └─────────────────┘                         └────────┬────────┘           │
│                                                       │                     │
└───────────────────────────────────────────────────────│─────────────────────┘
                                                        │
                                                        │ External Withdrawal
                                                        │ (this service)
                                                        ▼
                                              ┌─────────────────┐
                                              │ External Wallet │
                                              │ (Solana, etc.)  │
                                              └─────────────────┘
```

### Fee Handling

| Operation | Fee | Result |
|-----------|-----|--------|
| Sub-account → Master (internal) | Free | Master receives full amount |
| Master → External wallet | Deducted from amount | Recipient gets `amount - network_fee` |

The blockchain withdrawal fee is deducted from the withdrawal amount by Binance. No pre-calculation is required on the sending side.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                     MASTER WITHDRAWAL SERVICE                               │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌──────────────────┐     ┌──────────────────┐     ┌──────────────────┐    │
│  │  Transfer        │     │  Forwarding      │     │  Binance         │    │
│  │  Detector        │────▶│  Engine          │────▶│  Master API      │    │
│  │  (WebSocket)     │     │                  │     │  (REST)          │    │
│  └──────────────────┘     └──────────────────┘     └──────────────────┘    │
│           │                        │                                        │
│           │                        ▼                                        │
│           │               ┌──────────────────┐                             │
│           │               │  BoltDB Store    │                             │
│           │               │  (idempotency)   │                             │
│           │               └──────────────────┘                             │
│           │                                                                 │
│           ▼                                                                 │
│  ┌──────────────────────────────────────────────────────────────────────┐  │
│  │ Binance User Data Stream: wss://stream.binance.com:9443/ws/{listen}  │  │
│  │ (real-time balance updates on incoming transfers)                     │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Components

#### Transfer Detector

Monitors incoming sub-account transfers in real-time via Binance's User Data Stream WebSocket.

**WebSocket Connection:**
1. Obtain listen key: `POST /sapi/v1/userDataStream`
2. Connect to WebSocket: `wss://stream.binance.com:9443/ws/{listenKey}`
3. Refresh listen key every 30 minutes: `PUT /sapi/v1/userDataStream`

**Event Type:** `balanceUpdate`

When funds arrive from a sub-account transfer, Binance pushes a balance update event. The service then queries the transfer history to get the full transfer details including `tranId`.

**Fallback Polling:**

On startup (or WebSocket reconnection), poll recent transfer history to catch any transfers that arrived while disconnected:

```
GET /sapi/v1/sub-account/transfer/subUserHistory
  type=1 (transfers TO master)
  startTime={lastProcessedTime}
```

This ensures no transfers are missed during restarts or connection drops.

#### Forwarding Engine

Processes detected transfers:
1. Check BoltDB - skip if already processed
2. Query Binance withdrawal history to verify no prior withdrawal exists
3. Look up destination address for asset/sub-account (must be explicitly configured)
4. Initiate external withdrawal with `withdrawOrderId` set to `tranId`
5. Record result in store

**API Endpoints:**
- Query: `GET /sapi/v1/capital/withdraw/history?withdrawOrderId={tranId}`
- Withdraw: `POST /sapi/v1/capital/withdraw/apply`

#### BoltDB Store

Persistent key/value storage for:
- Processed transfer tracking (idempotency)
- Audit log of all forwarding actions
- Recovery state for crash scenarios

BoltDB is a pure-Go embedded database with no CGO dependency, simplifying builds and deployment.

## Idempotency

Critical requirement: never forward the same transfer twice.

### Two-Layer Protection

**Layer 1: Application-level (BoltDB)**

Every incoming transfer has a unique `tranId`. Before processing, check the local store for fast-path rejection of duplicates.

**Layer 2: Binance query verification**

Before initiating a withdrawal, query Binance's withdrawal history using `withdrawOrderId` to verify no withdrawal already exists. This catches cases where BoltDB state was lost or corrupted.

### Processing Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                    Processing Flow                              │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  1. Detect transfer (tranId: "abc123")                         │
│                     │                                           │
│                     ▼                                           │
│  2. Check BoltDB: seen "abc123"?                               │
│           │                    │                                │
│          YES                  NO                                │
│           │                    │                                │
│           ▼                    ▼                                │
│     Skip (already         3. Query Binance withdrawal history  │
│     processed)               GET /sapi/v1/capital/withdraw/    │
│                              history?withdrawOrderId=abc123     │
│                                │                                │
│                    ┌───────────┴───────────┐                   │
│                    │                       │                    │
│               Found                    Not Found                │
│                    │                       │                    │
│                    ▼                       ▼                    │
│              Update BoltDB           4. Insert "abc123" →      │
│              → FORWARDED                PENDING in BoltDB       │
│              (already done)                │                    │
│                                            ▼                    │
│                                      5. Execute withdrawal      │
│                                         withdrawOrderId=abc123  │
│                                            │                    │
│                                            ▼                    │
│                                      6. Update "abc123" →      │
│                                         FORWARDED              │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### Why Two Layers?

| Layer | Purpose | Protects Against |
|-------|---------|------------------|
| BoltDB | Fast-path duplicate detection | Normal duplicate processing |
| Binance query | Source-of-truth verification | BoltDB state loss, crashes, corruption |

The `withdrawOrderId` parameter is set to the incoming transfer's `tranId`, creating a consistent correlation key between internal transfers and outgoing withdrawals.

### State Machine

```
         ┌─────────┐
         │ UNKNOWN │  (not in store)
         └────┬────┘
              │ First detected
              ▼
         ┌─────────┐
         │ PENDING │  Recorded, withdrawal not yet initiated
         └────┬────┘
              │ Withdrawal API succeeded
              ▼
        ┌──────────┐
        │ FORWARDED│  Withdrawal initiated, have Binance refId
        └────┬─────┘
              │ Confirmed on-chain (optional tracking)
              ▼
        ┌──────────┐
        │ COMPLETED│  External withdrawal confirmed
        └──────────┘

         ┌─────────┐
         │ FAILED  │  Permanent failure (e.g., invalid address)
         └─────────┘
```

### Recovery on Startup

On service start:

1. Query BoltDB for `PENDING` records (crashed before initiating withdrawal)
2. For each, query Binance withdrawal history by `withdrawOrderId` (which equals `tranId`)
3. If withdrawal exists: update BoltDB state to `FORWARDED`
4. If not found: proceed with withdrawal

This is the same Binance verification used during normal processing, ensuring consistent behavior whether recovering from a crash or processing new transfers.

## Database Schema

BoltDB is a pure-Go key/value store. Data is organized into buckets with secondary index buckets for querying.

### Buckets

```
transfers/
  {tranId} → TransferRecord (JSON)

by-status/
  {status}:{tranId} → ""    (index for status queries)

by-time/
  {timestamp}:{tranId} → "" (index for time-ordered listing)
```

### TransferRecord Structure

```go
type TransferRecord struct {
    TranID       string    `json:"tran_id"`        // Binance internal transfer ID (primary key)
    SubAccount   string    `json:"sub_account"`    // Source sub-account email
    Asset        string    `json:"asset"`          // e.g., "SOL", "ETH"
    Amount       string    `json:"amount"`         // Decimal string for precision
    Status       string    `json:"status"`         // PENDING, FORWARDED, COMPLETED, FAILED

    // Destination info (captured at forward time)
    DestAddress  string    `json:"dest_address,omitempty"`
    DestNetwork  string    `json:"dest_network,omitempty"`

    // Binance withdrawal tracking
    WithdrawID   string    `json:"withdraw_id,omitempty"`   // Binance withdrawal ID
    WithdrawTxID string    `json:"withdraw_txid,omitempty"` // Blockchain transaction hash
    WithdrawFee  string    `json:"withdraw_fee,omitempty"`  // Fee deducted by Binance

    // Timestamps
    TransferTime time.Time `json:"transfer_time"`  // When Binance recorded the internal transfer
    DetectedAt   time.Time `json:"detected_at"`    // When we detected it
    ForwardedAt  time.Time `json:"forwarded_at,omitempty"`  // When withdrawal was initiated
    CompletedAt  time.Time `json:"completed_at,omitempty"`  // When withdrawal was confirmed

    // Error tracking
    Error      string `json:"error,omitempty"`  // Error message if FAILED
    RetryCount int    `json:"retry_count"`      // Number of retry attempts
}
```

### Index Maintenance

When updating a record's status, update the `by-status` index atomically:

```go
func (s *Store) UpdateStatus(tranId, oldStatus, newStatus string) error {
    return s.db.Update(func(tx *bbolt.Tx) error {
        // Remove old index entry
        tx.Bucket([]byte("by-status")).Delete([]byte(oldStatus + ":" + tranId))
        // Add new index entry
        tx.Bucket([]byte("by-status")).Put([]byte(newStatus + ":" + tranId), nil)
        // Update the record itself
        // ...
        return nil
    })
}
```

### Querying by Status

```go
func (s *Store) GetByStatus(status string) ([]TransferRecord, error) {
    var records []TransferRecord
    err := s.db.View(func(tx *bbolt.Tx) error {
        statusBucket := tx.Bucket([]byte("by-status"))
        transfersBucket := tx.Bucket([]byte("transfers"))

        prefix := []byte(status + ":")
        c := statusBucket.Cursor()
        for k, _ := c.Seek(prefix); bytes.HasPrefix(k, prefix); k, _ = c.Next() {
            tranId := bytes.TrimPrefix(k, prefix)
            data := transfersBucket.Get(tranId)
            var rec TransferRecord
            json.Unmarshal(data, &rec)
            records = append(records, rec)
        }
        return nil
    })
    return records, err
}
```

## Configuration

```yaml
# Binance API credentials (master account)
binance:
  api_key: ${BINANCE_MASTER_API_KEY}
  api_secret: ${BINANCE_MASTER_API_SECRET}
  base_url: "https://api.binance.com"  # or testnet

# WebSocket configuration
websocket:
  stream_url: "wss://stream.binance.com:9443/ws"
  listen_key_refresh: 30m   # Binance requires refresh every 60min, we do 30min for safety
  reconnect_delay: 5s       # Delay before reconnecting on disconnect
  startup_lookback: 5m      # How far back to check on startup/reconnect

# Destination mappings (each sub-account must be explicitly configured)
destinations:
  midas-prod@example.com:
    SOL:
      address: "7xKpR3gD..."
      network: "SOL"
    ETH:
      address: "0xAbCdEf..."
      network: "ETH"
    USDC:
      address: "0x123456..."
      network: "ARBITRUM"

  midas-staging@example.com:
    SOL:
      address: "8yLqS4hE..."
      network: "SOL"

# Database (BoltDB)
database:
  path: "./data/withdrawals.bolt"

# Logging
logging:
  level: "info"
  format: "json"
```

## API Endpoints (Optional HTTP Interface)

For monitoring and manual intervention:

```
GET  /health              - Health check
GET  /status              - Service status and metrics
GET  /transfers           - List recent transfers (with filters)
GET  /transfers/:tranId   - Get specific transfer details
POST /transfers/:tranId/retry  - Manually retry a failed transfer
```

## Security Considerations

### API Key Permissions

| Account | Required Permissions |
|---------|---------------------|
| Master | Enable Withdrawals, Enable Reading |
| Sub-accounts | Enable Internal Transfer (no withdrawal permission needed) |

### Operational Security

1. **Withdrawal address whitelisting**: Configure all destination addresses in Binance's whitelist. This prevents the service from withdrawing to unauthorized addresses even if compromised.

2. **Audit logging**: Log all forwarding actions at WARN level for audit trail:
   ```
   WITHDRAWAL_INITIATED: tranId=abc123, asset=SOL, amount=100, dest=7xKp...
   WITHDRAWAL_COMPLETED: tranId=abc123, txid=5nFg..., fee=0.001
   ```

3. **Alerting thresholds**: Configure alerts for:
   - High volume of failed forwards
   - Unconfigured sub-accounts or assets (funds stuck until configured)

### Credential Storage

- Store API credentials in environment variables or a secrets manager
- Never log or persist credentials
- Consider short-lived credentials if Binance supports them

## Metrics

Expose Prometheus metrics:

```
# Counter: transfers detected
binance_withdrawal_transfers_detected_total{sub_account, asset}

# Counter: withdrawals initiated
binance_withdrawal_forwards_total{sub_account, asset, status}

# Histogram: forwarding latency (detection → withdrawal initiated)
binance_withdrawal_forward_latency_seconds{asset}

# Gauge: pending transfers count
binance_withdrawal_pending_count

# Counter: errors by type
binance_withdrawal_errors_total{error_type}
```

## Error Handling

### Transient Errors (Retry)

- Network timeouts
- Binance API rate limits (429)
- Temporary service unavailable (503)
- WebSocket disconnection

Retry with exponential backoff, max 5 attempts.

### WebSocket Disconnection Handling

1. Attempt reconnection with exponential backoff
2. On successful reconnect, poll transfer history from last known timestamp
3. Resume normal WebSocket monitoring
4. If listen key expired, obtain a new one before reconnecting

### Permanent Errors (Fail)

- Sub-account not configured
- Asset not configured for sub-account
- Invalid destination address
- Asset not supported for withdrawal on Binance
- Insufficient balance in master account
- Amount below Binance's minimum withdrawal for the network

Mark as `FAILED` with error message. Require manual intervention (typically a configuration update, then retry).

### Edge Cases

1. **Master account insufficient balance**: Log error, mark as PENDING, retry on next poll cycle.

2. **Destination not configured for asset**: Log error, mark as FAILED. Requires configuration update.

3. **Sub-account not recognized**: Log error, mark as FAILED. Sub-account must be explicitly configured before funds can be forwarded.

4. **Duplicate tranId from Binance**: Should not happen, but BoltDB PRIMARY KEY constraint protects against it.

## Deployment

### Single Instance

Recommended for simplicity. BoltDB handles persistence.

```bash
./binance-withdrawal-service --config config.yaml
```

### Systemd Service

```ini
[Unit]
Description=Binance Master Withdrawal Service
After=network.target

[Service]
Type=simple
User=binance
ExecStart=/usr/local/bin/binance-withdrawal-service --config /etc/binance-withdrawal/config.yaml
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

### Docker

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o /binance-withdrawal-service .

FROM alpine:latest
RUN apk add --no-cache ca-certificates
COPY --from=builder /binance-withdrawal-service /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/binance-withdrawal-service"]
```

## Future Considerations

1. **Multi-instance support**: Replace BoltDB with Redis or PostgreSQL for distributed deployments.

2. **Deposit forwarding**: Reverse flow - detect deposits to master and transfer to sub-accounts.

3. **Manual approval workflow**: For large withdrawals, require human approval before forwarding.

4. **Withdrawal limits**: Add per-asset or USD-denominated limits if needed for risk management.

## References

- [Binance Withdraw API](https://developers.binance.com/docs/wallet/capital/withdraw)
- [Binance Withdraw History API](https://developers.binance.com/docs/wallet/capital/withdraw-history)
- [Binance Sub-Account Transfer API](https://developers.binance.com/docs/sub_account/asset-management/Sub-account-Transfer)
- [Binance User Data Stream](https://developers.binance.com/docs/binance-spot-api-docs/user-data-stream)
