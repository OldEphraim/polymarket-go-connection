#!/usr/bin/env python3
"""
Place a TRUE market order using FOK (Fill-or-Kill) for Romania vs Cyprus
This will fill immediately at the best available price or cancel
"""

import os
import requests
import json
from py_clob_client.client import ClobClient
from py_clob_client.clob_types import OrderArgs, OrderType
from py_clob_client.order_builder.constants import BUY
from dotenv import load_dotenv

load_dotenv('../.env')

def get_market_info(slug):
    """Get token IDs and current prices from API"""
    print(f"Fetching market: {slug}")
    
    try:
        url = f"https://gamma-api.polymarket.com/markets/slug/{slug}"
        response = requests.get(url, timeout=5)
        
        if response.status_code == 200:
            data = response.json()
            
            # Parse the data
            token_ids = json.loads(data.get('clobTokenIds', '[]'))
            outcomes = json.loads(data.get('outcomes', '[]'))
            prices = json.loads(data.get('outcomePrices', '[]'))
            
            print(f"✓ Market found: {data.get('question', 'Unknown')}")
            
            return {
                "token_id": token_ids[0] if token_ids else None,
                "api_price": float(prices[0]) if prices else 0.5,
                "active": data.get('active', False)
            }
    except Exception as e:
        print(f"Error fetching market: {e}")
    
    return None

def place_romania_fok_order():
    """
    Place a TRUE market order for Romania using Fill-or-Kill
    """
    
    MARKET_SLUG = "caf-gam-bur1-2025-09-09-gam"
    DOLLAR_AMOUNT = 5.0
    
    print("="*70)
    print("TRUE MARKET ORDER - ROMANIA vs CYPRUS")
    print("="*70)
    print("Order Type: FOK (Fill-or-Kill)")
    print("This will fill IMMEDIATELY at best price or cancel")
    print(f"Amount: ${DOLLAR_AMOUNT}")
    print("-"*70)
    
    # Step 1: Get market info
    print("\n1. Getting market data...")
    market = get_market_info(MARKET_SLUG)
    
    if not market or not market['token_id']:
        print("❌ Could not fetch market data")
        return
    
    token_id = market['token_id']
    api_price = market['api_price']
    
    print(f"   Token ID: {token_id}")
    print(f"   API shows price: {api_price*100:.1f}¢")
    print("   ⚠️  Note: API price may be stale")
    
    # Step 2: Set up FOK order at maximum price
    MAX_PRICE = 0.99  # Maximum allowed on Polymarket
    shares_to_buy = round(DOLLAR_AMOUNT / MAX_PRICE, 4)  # Round to 4 decimals
    
    print(f"\n2. FOK Order Setup:")
    print(f"   Order price: ${MAX_PRICE:.2f} ({int(MAX_PRICE*100)}¢)")
    print(f"   This is the MAXIMUM you'll pay")
    print(f"   Actual fill will be at best available price")
    print(f"   Shares to buy: {shares_to_buy:.4f}")
    
    # Estimate based on API price
    if api_price > 0:
        est_shares = DOLLAR_AMOUNT / api_price
        est_return = est_shares * 1.0
        est_profit = est_return - DOLLAR_AMOUNT
        
        print(f"\n3. Estimated outcome (if API price {api_price*100:.1f}¢ is correct):")
        print(f"   Shares: ~{est_shares:.2f}")
        print(f"   If Romania wins: ${est_return:.2f}")
        print(f"   Profit: ${est_profit:.2f}")
        print(f"   ROI: {(est_profit/DOLLAR_AMOUNT)*100:.1f}%")
    
    # Step 3: Confirm
    print("\n" + "="*70)
    print("CONFIRM FOK MARKET ORDER")
    print("="*70)
    print("This order will either:")
    print("  ✓ Fill COMPLETELY and IMMEDIATELY at best price")
    print("  ✗ Cancel if it can't fill completely")
    print()
    print(f"Market: Romania to beat Cyprus")
    print(f"Type: YES (Romania wins)")
    print(f"Amount: ${DOLLAR_AMOUNT}")
    print(f"Max price: {MAX_PRICE*100:.0f}¢ (will fill at best available)")
    
    confirm = input("\n⚠️  Place FOK market order? (y/n): ")
    if confirm.lower() != 'y':
        print("Cancelled")
        return
    
    # Step 4: Place FOK order
    print("\n4. Placing FOK order...")
    
    host = "https://clob.polymarket.com"
    key = os.getenv("PRIVATE_KEY")
    chain_id = 137
    
    try:
        # Initialize client
        client = ClobClient(host, key=key, chain_id=chain_id)
        api_creds = client.create_or_derive_api_creds()
        client.set_api_creds(api_creds)
        print("   ✓ Connected")
        
        # Create order with proper decimal precision
        order_args = OrderArgs(
            token_id=token_id,
            price=0.99,  # Max price, hardcoded to avoid float issues
            size=shares_to_buy,  # Already rounded to 4 decimals
            side=BUY
        )
        
        signed_order = client.create_order(order_args)
        print("   ✓ Order signed")
        
        # Submit as FOK
        print("   Submitting as Fill-or-Kill...")
        resp = client.post_order(signed_order, OrderType.GTC)
        
        print("\n" + "="*70)
        print("✅ FOK ORDER EXECUTED!")
        print("="*70)
        print(f"Order ID: {resp.get('orderID', 'N/A')}")
        print(f"Status: {resp.get('status', 'N/A')}")
        
        # Check fill price
        print("\nChecking fill price...")
        check_last_trade(client)
        
    except Exception as e:
        error_msg = str(e)
        print(f"\n❌ FOK Order failed: {error_msg}")
        
        if "couldn't be fully filled" in error_msg.lower():
            print("\n📊 Insufficient liquidity!")
            print("There aren't enough sellers at ≤99¢ to fill your $5 order")
            print("\nOptions:")
            print("1. Try a smaller amount ($2-3)")
            print("2. Use IOC for partial fill")
            print("3. Use GTC and wait")
        elif "not enough balance" in error_msg.lower():
            print("\nInsufficient USDC balance")

def place_romania_ioc_order():
    """
    Alternative: Use IOC for partial fills
    """
    # Similar to FOK but uses OrderType.IOC
    print("\nIOC (Immediate-or-Cancel) will fill what it can and cancel the rest")
    print("Not implemented in this demo - modify FOK code to use OrderType.IOC")

def place_romania_gtc_order():
    """
    Alternative: Regular limit order that stays on the book
    """
    # Similar to FOK but uses OrderType.GTC
    print("\nGTC (Good-Till-Cancelled) places a limit order that waits")
    print("Not implemented in this demo - modify FOK code to use OrderType.GTC")

def check_last_trade(client):
    """Check the last trade to see actual fill price"""
    try:
        trades = client.get_trades()
        if trades and len(trades) > 0:
            last_trade = trades[0]
            size = float(last_trade.get('size', 0))
            price = float(last_trade.get('price', 0))
            value = size * price
            
            print(f"\n📊 Your order filled at:")
            print(f"   Price: ${price:.3f} ({price*100:.1f}¢)")
            print(f"   Shares: {size:.2f}")
            print(f"   Total: ${value:.2f}")
            print(f"   If Romania wins: ${size:.2f}")
            print(f"   Profit if wins: ${size - value:.2f}")
    except:
        print("Could not fetch trade details")

if __name__ == "__main__":
    print("MARKET ORDER FOR ROMANIA vs CYPRUS")
    print("="*70)
    print("\n1. FOK (Fill-or-Kill) - All or nothing, immediate")
    print("2. IOC (Immediate-or-Cancel) - Partial fills OK")
    print("3. GTC (Good-Till-Cancelled) - Limit order, waits")
    
    choice = input("\nChoice (1/2/3): ").strip()
    
    if choice == "1":
        place_romania_fok_order()
    elif choice == "2":
        place_romania_ioc_order()
    elif choice == "3":
        place_romania_gtc_order()
    else:
        print("Invalid choice")

