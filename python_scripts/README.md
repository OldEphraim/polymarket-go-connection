# Polymarket Trading System (Python)

A comprehensive Python toolkit for programmatic trading on Polymarket using the CLOB API.

## 🚀 Quick Start

```bash
# Clone and setup
git clone <repository>
cd polymarket-trading
python -m venv venv
source venv/bin/activate  # On Windows: venv\Scripts\activate
pip install -r requirements.txt

# Configure
cp .env.example .env
# Edit .env with your private key and wallet address

# Check setup
python utils/check_balances.py
python setup/complete_approvals.py --check-only

# Find a market and trade
python utils/search_markets.py "bitcoin"
python utils/get_token_from_slug.py "will-bitcoin-hit-100k"
python core/place_trade.py --slug "will-bitcoin-hit-100k" --side buy --amount 5
```

## 📁 Project Structure

```
polymarket-trading/
├── core/                       # Core trading functionality
│   ├── place_trade.py         # Main trading interface
│   ├── portfolio_checker.py   # Portfolio monitoring
│   └── order_manager.py       # Order management
├── setup/                      # Setup and configuration
│   └── complete_approvals.py  # Contract approval setup
├── utils/                      # Utility scripts
│   ├── check_balances.py      # Balance checking
│   ├── get_token_from_slug.py # Market data fetcher
│   └── search_markets.py      # Market discovery
├── config.py                   # Configuration constants
├── requirements.txt            # Python dependencies
├── .env.example               # Environment template
└── README.md                  # This file
```

## 🔧 Installation

### Prerequisites

- Python 3.8+
- Polygon (MATIC) wallet with:
  - At least 0.1 MATIC for gas
  - USDC tokens for trading
  - Private key access

### Setup Steps

1. **Install dependencies:**
```bash
pip install web3 py-clob-client python-dotenv tabulate requests
```

2. **Configure environment:**
Create `.env` file:
```env
PRIVATE_KEY=your_private_key_here_without_0x
WALLET_ADDRESS=your_wallet_address_here
```

3. **Approve contracts (one-time):**
```bash
python setup/complete_approvals.py
```

## 📊 Core Scripts

### place_trade.py
Main trading interface for buying and selling.

```bash
# Buy $10 worth at market price
python core/place_trade.py --slug "market-slug" --side buy --amount 10

# Sell 5 shares at 75¢
python core/place_trade.py --slug "market-slug" --side sell --shares 5 --price 0.75

# Dry run (preview without placing)
python core/place_trade.py --slug "market-slug" --side buy --amount 10 --dry-run
```

**Options:**
- `--slug`: Market identifier
- `--side`: buy or sell
- `--amount`: Dollar amount (for buys)
- `--shares`: Number of shares (for sells)
- `--price`: Limit price (optional)
- `--order-type`: GTC, FOK, or IOC
- `--outcome`: Outcome index (0=Yes/first, 1=No/second)
- `--dry-run`: Preview without placing

### portfolio_checker.py
Monitor your complete portfolio.

```bash
# Basic view
python core/portfolio_checker.py

# Detailed with history
python core/portfolio_checker.py --detailed --show-history

# JSON output
python core/portfolio_checker.py --json > portfolio.json
```

### order_manager.py
Manage open orders.

```bash
# List all orders
python core/order_manager.py list

# Cancel specific order
python core/order_manager.py cancel 0x123abc...

# Cancel all orders
python core/order_manager.py cancel-all

# Live monitoring
python core/order_manager.py monitor --refresh 10
```

## 🔍 Utility Scripts

### search_markets.py
Find tradeable markets.

```bash
# Search by keyword
python utils/search_markets.py "election"

# Active markets only
python utils/search_markets.py "sports" --active-only

# Minimum volume filter
python utils/search_markets.py "crypto" --min-volume 10000
```

### get_token_from_slug.py
Get market details and token IDs.

```bash
# Get market info
python utils/get_token_from_slug.py "will-bitcoin-hit-100k"

# JSON output
python utils/get_token_from_slug.py "will-bitcoin-hit-100k" --json
```

### check_balances.py
Check all balances and approvals.

```bash
# One-time check
python utils/check_balances.py

# Continuous monitoring
python utils/check_balances.py --watch --interval 30
```

## 🔐 Setup Scripts

### complete_approvals.py
Approve contracts for trading (required once per wallet).

```bash
# Check current approvals
python setup/complete_approvals.py --check-only

# Approve for bridged USDC (most common)
python setup/complete_approvals.py

# Approve for native USDC.e
python setup/complete_approvals.py --usdc-type native
```

## 💡 Trading Examples

### Example 1: Simple Buy
```bash
# Find a market
python utils/search_markets.py "super bowl"

# Get market details
python utils/get_token_from_slug.py "super-bowl-winner-2024"

# Place buy order
python core/place_trade.py \
    --slug "super-bowl-winner-2024" \
    --side buy \
    --amount 20 \
    --outcome 0
```

### Example 2: Sell Position
```bash
# Check what you own
python core/portfolio_checker.py

# Place sell order
python core/place_trade.py \
    --slug "market-slug" \
    --side sell \
    --shares 10 \
    --price 0.85
```

### Example 3: Market Order
```bash
# Buy at any price up to 99¢
python core/place_trade.py \
    --slug "market-slug" \
    --side buy \
    --amount 50 \
    --order-type FOK
```

## ⚠️ Important Notes

### Order Types
- **GTC** (Good Till Cancelled): Default, stays open until filled
- **FOK** (Fill or Kill): Must fill completely immediately or cancels
- **IOC** (Immediate or Cancel): Fills what it can immediately, cancels rest

### Price Limits
- Maximum price: $0.99 (99¢)
- Minimum price: $0.01 (1¢)
- Price precision: 2 decimals
- Size precision: 4 decimals

### USDC Types
- **Bridged USDC**: Standard, most common (0x2791...)
- **Native USDC.e**: Polygon native (0x3c49...)
- Check which you have: `python utils/check_balances.py`

## 🐛 Troubleshooting

### "Not enough balance"
1. Check USDC balance: `python utils/check_balances.py`
2. Ensure contracts approved: `python setup/complete_approvals.py --check-only`
3. Wait a few minutes for API sync

### FOK orders failing
- Market lacks liquidity
- Try smaller amounts or use GTC

### Orders not showing
- API may have delay
- Check on polymarket.com/portfolio
- Use `python core/portfolio_checker.py`

### Gas issues
- Need minimum 0.1 MATIC
- Check with: `python utils/check_balances.py`

## 📈 Best Practices

1. **Start Small**: Test with $1-5 orders first
2. **Use Dry Run**: Preview orders with `--dry-run`
3. **Check Liquidity**: Use `search_markets.py --min-volume`
4. **Monitor Orders**: Use `order_manager.py monitor`
5. **Keep Gas**: Maintain at least 0.1 MATIC

## 🔄 Workflow Example

```bash
# 1. Setup (once)
python setup/complete_approvals.py

# 2. Find opportunity
python utils/search_markets.py "interesting topic"

# 3. Get market details
python utils/get_token_from_slug.py "market-slug"

# 4. Check your funds
python utils/check_balances.py

# 5. Place order
python core/place_trade.py --slug "market-slug" --side buy --amount 10

# 6. Monitor
python core/order_manager.py monitor

# 7. Check portfolio
python core/portfolio_checker.py
```

## 📝 Configuration

### Environment Variables (.env)
```env
PRIVATE_KEY=your_private_key_without_0x
WALLET_ADDRESS=your_wallet_address
```

### Constants (config.py)
- Contract addresses
- API endpoints
- Trading limits
- Gas settings

## 🚧 Known Issues

1. **API Price Lag**: Gamma API prices may be stale
2. **Decimal Precision**: Handled automatically but can cause issues
3. **Liquidity**: Many markets have low liquidity
4. **UI Sync**: Programmatic trades may not immediately show in web UI

## 📚 Resources

- [Polymarket Docs](https://docs.polymarket.com)
- [CLOB API Reference](https://docs.polymarket.com/developers)
- [Polygon Gas Tracker](https://polygonscan.com/gastracker)

## ⚖️ License

MIT

## 🤝 Contributing

Pull requests welcome. Please test thoroughly with small amounts first.

---

**Warning**: Trading involves risk. Start small, test thoroughly, and never trade more than you can afford to lose.