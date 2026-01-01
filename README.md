# AirFi - WiFi Access via CKB Micropayments

AirFi enables pay-per-use WiFi access using CKB (Nervos Network) micropayments with Perun state channels. Guests pay with cryptocurrency and get instant internet access controlled via OpenWrt/OpenNDS captive portal.

## Features

- **Crypto-Powered WiFi**: Pay with CKB, get instant internet access
- **OpenWrt/OpenNDS Integration**: Automatic MAC authorization via captive portal
- **Perun State Channels**: Off-chain micropayments (no gas per minute)
- **Auto Wallet Generation**: Temporary CKB wallet for each guest session
- **JWT Authentication**: Secure token-based WiFi access
- **Real-time Dashboard**: Live session monitoring for hosts
- **Configurable Pricing**: Set your own CKB per hour rate via dashboard
- **Background Settlement**: Fast disconnect, settlement runs in background
- **Auto-Refund**: Remaining CKB returned to sender when session ends

## Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│  OpenWrt Router │     │  Backend Server │     │   CKB Testnet   │
│  (OpenNDS)      │────▶│    (Golang)     │────▶│  (via Perun)    │
└─────────────────┘     └─────────────────┘     └─────────────────┘
        │                       │
        │ SSH (ndsctl)          │
        │◀──────────────────────│
        │                       ▼
        │               ┌─────────────────┐
        ▼               │  SQLite DB      │
┌─────────────────┐     │  (airfi.db)     │
│   Guest Device  │     └─────────────────┘
│   (Browser)     │
└─────────────────┘
```

### WiFi Access Flow

```
Guest connects to WiFi
        │
        ▼
OpenNDS intercepts traffic
        │
        ▼
Redirect to: http://airfi/?mac=aa:bb:cc:dd:ee:ff&ip=192.168.1.100
        │
        ▼
Guest sends CKB to generated wallet
        │
        ▼
Backend detects payment, opens Perun channel
        │
        ▼
Backend calls: ndsctl auth <mac>
        │
        ▼
Guest gets internet access!
        │
        ▼
Session expires/ends
        │
        ▼
Backend calls: ndsctl deauth <mac>
        │
        ▼
Remaining CKB refunded to guest
```

## Quick Start

### 1. Build

```bash
go mod tidy
go build -o backend ./cmd/backend
go build -o hostcli ./cmd/hostcli

# Or run directly without building:
go run ./cmd/backend/
```

### 2. Configure OpenWrt Router

```bash
# On OpenWrt router
opkg update
opkg install opennds

# Edit /etc/config/opennds
# Set fasremoteip to your backend server
# Configure redirect URL to include mac and ip parameters
```

### 3. Run Backend

```bash
# With OpenWrt router
export OPENWRT_ADDRESS=192.168.1.1
export OPENWRT_PASSWORD=your_router_password
./backend

# Without router (testing mode)
./backend
```

Backend starts on `http://localhost:8080`

### 4. Access Web Portal

| URL | Description |
|-----|-------------|
| `http://localhost:8080/` | Guest landing page |
| `http://localhost:8080/connect` | Buy WiFi page |
| `http://localhost:8080/dashboard` | Host dashboard (password: `airfi2025`) |

## Pricing

Pricing is **configurable** via the dashboard. Default rates:

| Amount | Duration |
|--------|----------|
| 600 CKB | ~1 hour (minimum at 100 CKB/hr) |
| 700 CKB | ~2 hours |
| 800 CKB | ~3 hours |

**Channel Setup**: ~500 CKB (reserved for Perun channel)

**Rate**: Configurable per hour (default: 500 CKB/hour)

**Minimum**: Channel setup + 1 hour rate (e.g., 500 + 100 = 600 CKB at 100 CKB/hr)

> Pricing is fetched dynamically from `/api/v1/settings` and displayed on the landing page.

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | Server port |
| `HOST_PRIVATE_KEY` | (demo key) | Host wallet private key (hex) |
| `DASHBOARD_PASSWORD` | `airfi2025` | Dashboard login password |
| `DB_PATH` | `./airfi.db` | SQLite database path |

### OpenWrt/OpenNDS Router

| Variable | Default | Description |
|----------|---------|-------------|
| `OPENWRT_ADDRESS` | - | Router IP (required to enable) |
| `OPENWRT_PORT` | `22` | SSH port |
| `OPENWRT_USERNAME` | `root` | SSH username |
| `OPENWRT_PASSWORD` | - | SSH password |
| `OPENWRT_PRIVATE_KEY` | - | SSH private key (alternative) |
| `OPENWRT_AUTH_TIMEOUT` | `0` | Session timeout (0 = OpenNDS default) |

### Example

```bash
export PORT=8080
export DASHBOARD_PASSWORD=mysecretpassword
export OPENWRT_ADDRESS=192.168.1.1
export OPENWRT_PASSWORD=routerpass
./backend
```

## API Endpoints

### Guest Wallet

| Endpoint | Method | Description |
|----------|--------|-------------|
| `POST /api/v1/wallet/guest` | POST | Generate new guest wallet |
| `GET /api/v1/wallet/guest/:id` | GET | Check wallet status & balance |

### Session Management

| Endpoint | Method | Description |
|----------|--------|-------------|
| `GET /api/v1/sessions` | GET | List all sessions |
| `GET /api/v1/sessions/:id` | GET | Get session info |
| `GET /api/v1/sessions/:id/token` | GET | Get JWT access token |
| `POST /api/v1/sessions/:id/end` | POST | End session, settle channel |
| `POST /api/v1/sessions/:id/extend` | POST | Micropayment extension |

### Authentication

| Endpoint | Method | Description |
|----------|--------|-------------|
| `POST /api/v1/auth/validate` | POST | Validate JWT token |

### Settings

| Endpoint | Method | Description |
|----------|--------|-------------|
| `GET /api/v1/settings` | GET | Get current pricing settings (public) |
| `POST /api/v1/settings` | POST | Update pricing settings (auth required) |

### System

| Endpoint | Method | Description |
|----------|--------|-------------|
| `GET /health` | GET | Health check |
| `GET /api/v1/wallet` | GET | Host wallet status |

## Host CLI Commands

```bash
# Interactive dashboard with real-time updates
./hostcli dashboard

# Display QR code for guest portal
./hostcli qr

# List all sessions
./hostcli sessions

# Watch sessions (auto-refresh)
./hostcli sessions watch

# Get JWT token for a session
./hostcli token <session-id>

# System status
./hostcli status

# Wallet info
./hostcli wallet

# Settle channel manually
./hostcli settle <session-id>

# Custom API URL
./hostcli --api http://192.168.1.100:8080 dashboard
```

## Project Structure

```
airfi-perun-nervous/
├── cmd/
│   ├── backend/              # Backend server
│   │   ├── main.go           # Entry point
│   │   ├── server.go         # Server struct & initialization
│   │   ├── handlers.go       # Page handlers (HTML)
│   │   ├── handlers_api.go   # API handlers (JSON)
│   │   ├── session.go        # Session management & micropayments
│   │   ├── wallet.go         # Wallet operations & funding detection
│   │   ├── channel.go        # Perun channel operations
│   │   └── utils.go          # Utility functions
│   └── hostcli/              # Host CLI tool
├── internal/
│   ├── auth/                 # JWT authentication
│   ├── db/                   # SQLite database
│   ├── guest/                # Guest wallet generation
│   ├── perun/                # Perun channel integration
│   └── router/               # WiFi router control (OpenWrt)
├── web/guest/
│   ├── static/               # CSS, JS assets
│   └── templates/            # HTML templates
├── keys/                     # JWT keys (auto-generated)
└── airfi.db                  # SQLite database (auto-created)
```

## Database Schema

### Sessions Table

```sql
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    wallet_id TEXT,
    channel_id TEXT,
    guest_address TEXT,
    host_address TEXT,
    funding_ckb INTEGER DEFAULT 0,
    balance_ckb INTEGER DEFAULT 0,
    spent_ckb INTEGER DEFAULT 0,
    created_at DATETIME,
    expires_at DATETIME,
    status TEXT DEFAULT 'pending_funding',
    settled_at DATETIME,
    mac_address TEXT,
    ip_address TEXT
);
```

### Guest Wallets Table

```sql
CREATE TABLE guest_wallets (
    id TEXT PRIMARY KEY,
    address TEXT UNIQUE,
    private_key_hex TEXT,
    funding_ckb INTEGER DEFAULT 0,
    balance_ckb INTEGER DEFAULT 0,
    created_at DATETIME,
    funded_at DATETIME,
    session_id TEXT,
    status TEXT DEFAULT 'created',
    sender_address TEXT,
    mac_address TEXT,
    ip_address TEXT
);
```

## Session Status Flow

```
created → funded → channel_opening → active → settling → settled
                                        ↓
                                     expired → settling → settled
```

- **created**: Wallet generated, waiting for CKB
- **funded**: CKB received, opening channel
- **channel_opening**: Perun channel being set up
- **active**: WiFi access granted (MAC authorized)
- **expired**: Time ran out, auto-settling
- **settling**: Channel settlement in progress (background)
- **settled**: Channel closed, CKB refunded

> Settlement runs in the background so users can disconnect quickly without waiting for blockchain confirmation.

## OpenWrt/OpenNDS Setup

### Install OpenNDS

```bash
opkg update
opkg install opennds
```

### Configure OpenNDS

Edit `/etc/config/opennds`:

```
config opennds
    option enabled '1'
    option gatewayinterface 'br-lan'
    option fasport '80'
    option fasremoteip '192.168.1.100'  # Your backend server
    option faspath '/connect'
    option fas_secure_enabled '0'
```

### Configure Redirect with MAC/IP

OpenNDS will redirect guests to:
```
http://192.168.1.100/connect?mac=$clientmac&ip=$clientip
```

## Technology Stack

- **Backend**: Go 1.22+, Gin, SQLite
- **Blockchain**: Nervos CKB Testnet
- **State Channels**: Perun Network
- **Router**: OpenWrt with OpenNDS
- **Frontend**: HTML, CSS, JavaScript

### Dependencies

```
github.com/nervosnetwork/ckb-sdk-go/v2  # CKB SDK
github.com/gin-gonic/gin                 # HTTP framework
github.com/mattn/go-sqlite3              # SQLite driver
github.com/golang-jwt/jwt/v5             # JWT
golang.org/x/crypto/ssh                  # SSH for router control
go.uber.org/zap                          # Logging
perun.network/go-perun                   # State channels
```

## Testing

### Without Router (Mock Mode)

```bash
# Start backend without OPENWRT_ADDRESS
./backend

# Open browser
open http://localhost:8080/connect

# Send CKB to the generated wallet address
# Session activates automatically
```

### With Router

```bash
# Configure router
export OPENWRT_ADDRESS=192.168.1.1
export OPENWRT_PASSWORD=routerpass
./backend

# Connect device to WiFi
# OpenNDS redirects to portal
# Pay and get internet access
```

### API Tests

```bash
# Health check
curl http://localhost:8080/health

# Host wallet status
curl http://localhost:8080/api/v1/wallet

# List sessions
curl http://localhost:8080/api/v1/sessions

# Generate guest wallet
curl -X POST http://localhost:8080/api/v1/wallet/guest

# Check wallet status
curl http://localhost:8080/api/v1/wallet/guest/<wallet_id>

# Get session token
curl http://localhost:8080/api/v1/sessions/<session_id>/token
```

## CKB Testnet Resources

- **Explorer**: https://pudge.explorer.nervos.org
- **Faucet**: https://faucet.nervos.org
- **Documentation**: https://docs.nervos.org

## License

MIT License

## Links

- [Nervos CKB](https://nervos.org)
- [Perun Network](https://perun.network)
- [OpenNDS](https://github.com/openNDS/openNDS)
- [OpenWrt](https://openwrt.org)
