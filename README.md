# Binance Master Withdrawal Service

A microservice that automatically forwards funds from a Binance master account to configured external wallets. This enables sub-accounts (which cannot withdraw externally due to Binance platform restrictions) to effectively perform withdrawals by transferring funds to the master account.

## How It Works

```
Sub-Account (Midas)  ──[internal transfer]──▶  Master Account  ──[this service]──▶  External Wallet
                          (instant, free)                        (auto-forward)
```

1. Sub-account performs an internal transfer to the master account
2. This service detects the incoming transfer via WebSocket
3. Looks up the configured destination for the asset/sub-account
4. Initiates an external withdrawal with master account credentials

## Prerequisites

- Go 1.23.0 or later
- Binance master account with:
  - API key with **Enable Withdrawals** and **Enable Reading** permissions
  - Withdrawal address whitelist configured for security
- Sub-accounts configured with internal transfer permissions

## Installation

### From Source

```bash
git clone https://github.com/binance-withdrawal.git
cd binance-withdrawal
go build -o binance-withdrawal ./cmd/service
```

### Using Go Install

```bash
go install github.com/binance-withdrawal/cmd/service@latest
```

## Configuration

Copy the example configuration and customize for your environment:

```bash
cp config.example.yaml config.yaml
```

### Configuration File

```yaml
# Binance API credentials (master account)
binance:
  api_key: ${BINANCE_MASTER_API_KEY}
  api_secret: ${BINANCE_MASTER_API_SECRET}
  base_url: "https://api.binance.com"  # Use testnet for testing

# WebSocket configuration for real-time transfer detection
websocket:
  stream_url: "wss://stream.binance.com:9443/ws"
  listen_key_refresh: 30m
  reconnect_delay: 5s
  startup_lookback: 5m    # Check for missed transfers on startup

# Destination mappings - each sub-account must be explicitly configured
destinations:
  user@example.com:
    SOL:
      address: "7xKpR3gD..."
      network: "SOL"
    ETH:
      address: "0xAbCdEf..."
      network: "ETH"

# Database (BoltDB)
database:
  path: "./data/withdrawals.bolt"

# HTTP server for monitoring
server:
  address: ":8080"

# Completion poller
poller:
  interval: 30s

# Logging
logging:
  level: "info"    # debug, info, warn, error
  format: "json"   # json or text
```

### Environment Variables

API credentials can be provided via environment variables:

```bash
export BINANCE_MASTER_API_KEY="your-api-key"
export BINANCE_MASTER_API_SECRET="your-api-secret"
```

## Usage

### Running the Service

```bash
./binance-withdrawal --config config.yaml
```

### Command Line Options

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.yaml` | Path to configuration file |

## API Endpoints

The service exposes an HTTP API for monitoring and manual intervention:

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/health` | Health check |
| `GET` | `/status` | Service status and metrics |
| `GET` | `/metrics` | Prometheus metrics |
| `GET` | `/transfers` | List recent transfers |
| `GET` | `/transfers?status=PENDING` | Filter by status |
| `GET` | `/transfers/{id}` | Get specific transfer |
| `POST` | `/transfers/{id}/retry` | Retry a failed transfer |

### Example Responses

**GET /health**
```json
{
  "status": "ok",
  "timestamp": "2024-01-15T10:30:00Z"
}
```

**GET /status**
```json
{
  "timestamp": "2024-01-15T10:30:00Z",
  "uptime": "2h30m15s",
  "detector": {
    "connected": true,
    "last_processed_time": "2024-01-15T10:29:55Z"
  },
  "transfers": {
    "total": 150,
    "by_status": {
      "COMPLETED": 142,
      "FORWARDED": 5,
      "PENDING": 2,
      "FAILED": 1
    }
  }
}
```

**GET /transfers/{id}**
```json
{
  "tran_id": "abc123",
  "sub_account": "user@example.com",
  "asset": "SOL",
  "amount": "100.5",
  "status": "COMPLETED",
  "dest_address": "7xKpR3gD...",
  "dest_network": "SOL",
  "withdraw_id": "xyz789",
  "withdraw_txid": "5nFg...",
  "withdraw_fee": "0.001",
  "transfer_time": "2024-01-15T10:00:00Z",
  "forwarded_at": "2024-01-15T10:00:05Z",
  "completed_at": "2024-01-15T10:02:30Z"
}
```

## Deployment

### Binary

```bash
./binance-withdrawal --config /etc/binance-withdrawal/config.yaml
```

### Systemd

Create `/etc/systemd/system/binance-withdrawal.service`:

```ini
[Unit]
Description=Binance Master Withdrawal Service
After=network.target

[Service]
Type=simple
User=binance
ExecStart=/usr/local/bin/binance-withdrawal --config /etc/binance-withdrawal/config.yaml
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
sudo systemctl enable binance-withdrawal
sudo systemctl start binance-withdrawal
```

### Docker

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o /binance-withdrawal ./cmd/service

FROM alpine:latest
RUN apk add --no-cache ca-certificates
COPY --from=builder /binance-withdrawal /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/binance-withdrawal"]
```

Build and run:

```bash
docker build -t binance-withdrawal .
docker run -v $(pwd)/config.yaml:/etc/config.yaml \
  -e BINANCE_MASTER_API_KEY=xxx \
  -e BINANCE_MASTER_API_SECRET=xxx \
  binance-withdrawal --config /etc/config.yaml
```

## Monitoring

### Prometheus Metrics

The service exposes metrics at `/metrics`:

- `binance_withdrawal_transfers_detected_total` - Transfers detected by sub-account and asset
- `binance_withdrawal_forwards_total` - Withdrawals initiated by status
- `binance_withdrawal_forward_latency_seconds` - Time from detection to withdrawal
- `binance_withdrawal_pending_count` - Current pending transfers
- `binance_withdrawal_errors_total` - Errors by type

### Logs

Structured logging in JSON format (configurable):

```json
{"time":"2024-01-15T10:00:05Z","level":"INFO","msg":"transfer processed","tran_id":"abc123","status":"FORWARDED"}
```

## Security

- **Address Whitelisting**: Configure all destination addresses in Binance's withdrawal whitelist
- **Credential Storage**: Use environment variables or a secrets manager for API credentials
- **Audit Logging**: All forwarding actions are logged for audit trails
- **Idempotency**: Two-layer protection (local DB + Binance API) prevents duplicate withdrawals

## Architecture

See [DESIGN.md](DESIGN.md) for detailed architecture documentation including:

- Transfer detection via WebSocket
- Idempotency guarantees
- State machine and recovery
- Error handling strategies

## License

MIT
