# Polymarket Python Trading System - Complete Testing Guide

## Pre-Testing Setup

### 1. Environment Preparation
```bash
# Create project structure
mkdir -p polymarket-trading/{core,setup,utils}
cd polymarket-trading

# Setup Python environment
python3 -m venv venv
source venv/bin/activate  # Windows: venv\Scripts\activate

# Install dependencies
pip install web3 py-clob-client python-dotenv tabulate requests

# Save dependencies
pip freeze > requirements.txt
```

### 2. File Organization
Place scripts in correct directories:
```
polymarket-trading/
├── core/
│   ├── place_trade.py
│   ├── portfolio_checker.py
│   └── order_manager.py
├── setup/
│   └── complete_approvals.py
├── utils/
│   ├── check_balances.py
│   ├── get_token_from_slug.py
│   └── search_markets.py
├── config.py
├── .env
└── requirements.txt
```

### 3. Environment Configuration
Create `.env` file:
```env
PRIVATE_KEY=your_private_key_here_no_0x_prefix
WALLET_ADDRESS=0xYourWalletAddressHere
```

### 4. Pre-flight Checks
```bash
# Verify wallet has funds
python utils/check_balances.py

# Expected output should show:
# - MATIC balance > 0.1
# - USDC balance > 0 (either bridged or native)
```

---

## Phase 1: Read-Only Testing (Safe)

### Test 1.1: Balance Checking
```bash
# Basic balance check
python utils/check_balances.py

# JSON output
python utils/check_balances.py --json

# Continuous monitoring (Ctrl+C to stop)
python utils/check_balances.py --watch --interval 10
```

**Expected Results:**
- Shows MATIC balance
- Shows USDC balances (bridged and/or native)
- Shows approval status for contracts
- Indicates if ready to trade

**Success Criteria:**
- [ ] Script runs without errors
- [ ] Correctly identifies USDC type
- [ ] Shows accurate balances
- [ ] Displays approval status

### Test 1.2: Portfolio Checking
```bash
# Basic portfolio
python core/portfolio_checker.py

# Detailed view
python core/portfolio_checker.py --detailed

# With trade history
python core/portfolio_checker.py --show-history

# JSON format
python core/portfolio_checker.py --json
```

**Expected Results:**
- Shows all CTF token positions
- Lists open orders
- Displays recent trades
- Shows USDC and MATIC balances

**Success Criteria:**
- [ ] Displays current positions accurately
- [ ] Shows open orders if any exist
- [ ] Trade history matches web interface

### Test 1.3: Approval Status Check
```bash
python setup/complete_approvals.py --check-only
```

**Expected Results:**
- Shows approval status for each contract
- Indicates which USDC type is active
- Shows if ready to trade

**Success Criteria:**
- [ ] Correctly identifies approved contracts
- [ ] Shows proper USDC type
- [ ] No errors accessing blockchain

---

## Phase 2: Market Discovery Testing

### Test 2.1: Market Search
```bash
# Search for active markets
python utils/search_markets.py "bitcoin"

# Active markets only
python utils/search_markets.py "election" --active-only

# With minimum volume
python utils/search_markets.py "sports" --min-volume 10000

# JSON output
python utils/search_markets.py "crypto" --json --limit 5
```

**Expected Results:**
- Lists matching markets
- Shows volume and liquidity
- Indicates active/closed status
- Provides market slugs

**Success Criteria:**
- [ ] Finds relevant markets
- [ ] Filters work correctly
- [ ] Slugs are valid

### Test 2.2: Get Token Information
```bash
# Get market details (use slug from search)
python utils/get_token_from_slug.py "will-bitcoin-hit-100k-in-2024"

# JSON format
python utils/get_token_from_slug.py "will-bitcoin-hit-100k-in-2024" --json

# Raw API response
python utils/get_token_from_slug.py "will-bitcoin-hit-100k-in-2024" --raw
```

**Expected Results:**
- Shows market question
- Lists all outcomes with token IDs
- Displays current prices
- Shows if accepting orders

**Success Criteria:**
- [ ] Token IDs are valid (long numbers)
- [ ] Prices are between 0.01 and 0.99
- [ ] Shows correct number of outcomes

---

## Phase 3: Order Management Testing (No Money)

### Test 3.1: List Orders
```bash
# List all open orders
python core/order_manager.py list

# Verbose listing
python core/order_manager.py list --verbose
```

**Expected Results:**
- Shows all open orders
- Displays order details
- Shows remaining amounts

**Success Criteria:**
- [ ] Orders match web interface
- [ ] Details are accurate
- [ ] No connection errors

### Test 3.2: Order Monitoring
```bash
# Monitor orders (Ctrl+C to stop)
python core/order_manager.py monitor --refresh 5
```

**Expected Results:**
- Live updates every 5 seconds
- Shows order status changes
- Displays fill progress

**Success Criteria:**
- [ ] Updates correctly
- [ ] Clean display
- [ ] Ctrl+C exits cleanly

---

## Phase 4: Trading Testing (Start Small)

### Test 4.1: Dry Run Testing
```bash
# Find a liquid market first
python utils/search_markets.py "popular topic" --active-only --min-volume 1000

# Get token info
python utils/get_token_from_slug.py "market-slug"

# Dry run buy order
python core/place_trade.py \
    --slug "market-slug" \
    --side buy \
    --amount 1 \
    --dry-run

# Dry run sell order (if you have shares)
python core/place_trade.py \
    --slug "market-slug" \
    --side sell \
    --shares 1 \
    --price 0.50 \
    --dry-run
```

**Expected Results:**
- Shows order details without placing
- Calculates shares correctly
- Displays total cost/return

**Success Criteria:**
- [ ] Math is correct
- [ ] No actual order placed
- [ ] Shows all relevant info

### Test 4.2: Small Buy Order ($1)
```bash
# Place $1 limit buy order
python core/place_trade.py \
    --slug "liquid-market-slug" \
    --side buy \
    --amount 1 \
    --price 0.10 \
    --outcome 0

# Verify order placed
python core/order_manager.py list
```

**Expected Results:**
- Order placed successfully
- Order ID returned
- Order appears in manager

**Success Criteria:**
- [ ] Order placed without errors
- [ ] Order ID is valid
- [ ] Order shows in list
- [ ] Amount is correct ($1)

### Test 4.3: Cancel Order
```bash
# List orders to get ID
python core/order_manager.py list

# Cancel specific order (use partial ID)
python core/order_manager.py cancel 0x123...

# Verify cancelled
python core/order_manager.py list
```

**Expected Results:**
- Order cancelled successfully
- No longer in order list

**Success Criteria:**
- [ ] Cancel succeeds
- [ ] Order removed from list
- [ ] No errors

---

## Phase 5: Order Types Testing

### Test 5.1: GTC Order (Default)
```bash
# Good Till Cancelled order
python core/place_trade.py \
    --slug "market-slug" \
    --side buy \
    --amount 1 \
    --price 0.05 \
    --order-type GTC
```

**Expected Results:**
- Order stays open
- Shows in order list
- Remains until cancelled or filled

**Success Criteria:**
- [ ] Order placed
- [ ] Stays open
- [ ] Can be cancelled

### Test 5.2: FOK Order (Fill or Kill)
```bash
# Fill or Kill order (needs liquidity)
python core/place_trade.py \
    --slug "liquid-market" \
    --side buy \
    --amount 1 \
    --order-type FOK
```

**Expected Results:**
- Either fills completely or cancels
- Immediate result
- No partial fills

**Success Criteria:**
- [ ] Executes immediately
- [ ] All or nothing behavior
- [ ] Clear success/failure message

---

## Phase 6: Sell Order Testing

### Test 6.1: Check Positions
```bash
# See what you can sell
python core/portfolio_checker.py --detailed
```

**Expected Results:**
- Shows all CTF positions
- Displays share counts
- Shows token IDs

### Test 6.2: Place Sell Order
```bash
# Sell shares you own
python core/place_trade.py \
    --slug "market-where-you-have-shares" \
    --side sell \
    --shares 0.5 \
    --price 0.90
```

**Expected Results:**
- Sell order placed
- Order shows in list

**Success Criteria:**
- [ ] Order placed successfully
- [ ] Correct share amount
- [ ] Price within limits

---

## Phase 7: Comprehensive Testing

### Test 7.1: Full Workflow
```bash
# 1. Check balances
python utils/check_balances.py

# 2. Search for market
python utils/search_markets.py "test topic" --active-only

# 3. Get market details
python utils/get_token_from_slug.py "chosen-market-slug"

# 4. Place buy order
python core/place_trade.py --slug "chosen-market-slug" --side buy --amount 2

# 5. Monitor order
python core/order_manager.py monitor --refresh 5

# 6. Check portfolio
python core/portfolio_checker.py

# 7. Place sell order (if buy filled)
python core/place_trade.py --slug "chosen-market-slug" --side sell --shares 1 --price 0.80

# 8. Cancel remaining orders
python core/order_manager.py cancel-all
```

**Success Criteria:**
- [ ] All steps complete without errors
- [ ] Orders behave as expected
- [ ] Portfolio updates correctly

### Test 7.2: Error Handling
Test various error conditions:

```bash
# Insufficient balance
python core/place_trade.py --slug "market" --side buy --amount 1000000

# Invalid price
python core/place_trade.py --slug "market" --side buy --amount 1 --price 1.50

# Non-existent market
python utils/get_token_from_slug.py "fake-market-slug-123"

# Sell more shares than owned
python core/place_trade.py --slug "market" --side sell --shares 99999
```

**Expected Results:**
- Clear error messages
- No crashes
- Helpful suggestions

**Success Criteria:**
- [ ] All errors handled gracefully
- [ ] Informative error messages
- [ ] No uncaught exceptions

---

## Phase 8: Approval Testing (If Needed)

### Test 8.1: Approve Contracts
Only run if approvals needed:

```bash
# Check current status
python setup/complete_approvals.py --check-only

# If not approved, run approval
python setup/complete_approvals.py

# For native USDC.e
python setup/complete_approvals.py --usdc-type native
```

**Expected Results:**
- Shows gas estimate
- Confirms before proceeding
- Approves all contracts

**Success Criteria:**
- [ ] All contracts approved
- [ ] Gas fees reasonable
- [ ] Can trade after approval

---

## Testing Checklist Summary

### Core Functionality
- [ ] Balance checking works
- [ ] Portfolio display accurate
- [ ] Market search functional
- [ ] Token info retrieval works
- [ ] Buy orders place correctly
- [ ] Sell orders work
- [ ] Order cancellation works
- [ ] Order monitoring updates

### Order Types
- [ ] GTC orders stay open
- [ ] FOK orders fill or cancel
- [ ] Dry run prevents execution

### Error Handling
- [ ] Insufficient balance handled
- [ ] Invalid prices rejected
- [ ] Non-existent markets caught
- [ ] Decimal precision handled

### Integration
- [ ] Full workflow completes
- [ ] Data consistency across scripts
- [ ] Web UI matches script data

---

## Troubleshooting Guide

### Issue: "not enough balance"
```bash
# Check balances
python utils/check_balances.py

# Check approvals
python setup/complete_approvals.py --check-only
```

### Issue: FOK orders always fail
- Market lacks liquidity
- Try smaller amounts
- Use GTC instead

### Issue: Orders not showing
```bash
# Force refresh
python core/portfolio_checker.py --detailed

# Check on web
# https://polymarket.com/portfolio
```

### Issue: Script crashes
```bash
# Check dependencies
pip install -r requirements.txt

# Verify environment
python --version  # Should be 3.8+
```

---

## Post-Testing Cleanup

```bash
# Cancel all test orders
python core/order_manager.py cancel-all --confirm

# Final portfolio check
python core/portfolio_checker.py

# Save test results
python core/portfolio_checker.py --json > test_results.json
```

---

## Notes for Go Migration

Document these discovered behaviors:
1. Size parameter = shares, not dollars
2. Price limits: 0.01 to 0.99
3. Decimal limits: price 2, size 4
4. FOK needs sufficient liquidity
5. API prices can be stale
6. Orders may not immediately appear in UI

---

## Safety Reminders

1. **Start with $1-2 orders**
2. **Use dry-run first**
3. **Keep 0.1+ MATIC for gas**
4. **Cancel test orders promptly**
5. **Monitor positions after trades**
6. **Document any issues found**

---

This testing guide should be followed sequentially, starting with read-only operations and gradually increasing complexity. Each phase builds on the previous one, ensuring comprehensive testing of all functionality.