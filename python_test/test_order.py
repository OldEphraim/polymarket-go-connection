#!/usr/bin/env python3
"""
Place $5 market buy on Jordan to win vs Dominican Republic
"""

import os
import subprocess
import json
import requests
from py_clob_client.client import ClobClient
from py_clob_client.clob_types import OrderArgs, OrderType
from py_clob_client.order_builder.constants import BUY
from dotenv import load_dotenv

load_dotenv('../.env')

def get_token_info(slug):
    """Get token IDs and current prices"""
    print(f"Getting market info for: {slug}")
    
    try:
        # Use the gamma API to get market data
        url = f"https://gamma-api.polymarket.com/markets/slug/{slug}"
        response = requests.get(url, timeout=5)
        
        if response.status_code == 200:
            data = response.json()
            
            # Parse token IDs and prices
            token_ids = json.loads(data.get('clobTokenIds', '[]'))
            outcomes = json.loads(data.get('outcomes', '[]'))
            prices = json.loads(data.get('outcomePrices', '[]'))
            
            return {
                "Yes": {
                    "token_id": token_ids[0] if token_ids else None,
                    "outcome": outcomes[0] if outcomes else "Yes",
                    "price": float(prices[0]) if prices else 0.5
                },
                "No": {
                    "token_id": token_ids[1] if len(token_ids) > 1 else None,
                    "outcome": outcomes[1] if len(outcomes) > 1 else "No",
                    "price": float(prices[1]) if len(prices) > 1 else 0.5
                }
            }
    except Exception as e:
        print(f"Error getting market data: {e}")
        return None

def place_jordan_market_buy():
    """Place $5 market buy on Jordan to win"""
    
    MARKET_SLUG = "fif-jor-dom-2025-09-09-jor"
    DOLLAR_AMOUNT = 5.0
    
    print("="*70)
    print("JORDAN vs DOMINICAN REPUBLIC - MARKET BUY")
    print("="*70)
    print("Jordan is up 2-0")
    print(f"Placing ${DOLLAR_AMOUNT} on YES (Jordan wins)")
    print("-"*70)
    
    # Step 1: Get market info
    print("\n1. Fetching market data...")
    market_info = get_token_info(MARKET_SLUG)
    
    if not market_info:
        print("❌ Could not fetch market data")
        return
    
    yes_info = market_info["Yes"]
    token_id = yes_info["token_id"]
    current_price = yes_info["price"]
    
    print(f"   ✓ Token ID: {token_id}")
    print(f"   ✓ Current price: {current_price*100:.1f}¢")
    
    # Step 2: Calculate order
    # Use aggressive price to ensure fill
    order_price = min(0.999, current_price + 0.01)  # Max allowed is 0.999
    shares_to_buy = DOLLAR_AMOUNT / order_price
    
    print(f"\n2. Order calculation:")
    print(f"   Strategy: Aggressive limit order at {order_price*100:.1f}¢")
    print(f"   This will fill at best available price (likely ~{current_price*100:.1f}¢)")
    print(f"   Shares to buy: {shares_to_buy:.2f}")
    print(f"   Max cost: ${DOLLAR_AMOUNT:.2f}")
    
    # Estimate actual fill
    estimated_shares = DOLLAR_AMOUNT / current_price
    estimated_return = estimated_shares * 1.0  # If Jordan wins
    estimated_profit = estimated_return - DOLLAR_AMOUNT
    
    print(f"\n3. Expected outcome (if filled at {current_price*100:.1f}¢):")
    print(f"   Shares: {estimated_shares:.2f}")
    print(f"   If Jordan wins: ${estimated_return:.2f}")
    print(f"   Profit: ${estimated_profit:.2f}")
    print(f"   ROI: {(estimated_profit/DOLLAR_AMOUNT)*100:.1f}%")
    
    # Step 3: Confirm
    print("\n" + "="*70)
    print("CONFIRM ORDER")
    print("="*70)
    print(f"Market: Jordan to beat Dominican Republic")
    print(f"Current score: Jordan 2-0")
    print(f"Betting: YES (Jordan wins)")
    print(f"Amount: ${DOLLAR_AMOUNT}")
    print(f"Order price: {order_price*100:.1f}¢ (will fill at ~{current_price*100:.1f}¢)")
    print(f"Shares: ~{estimated_shares:.2f}")
    
    confirm = input("\n⚠️  Place this order? (y/n): ")
    if confirm.lower() != 'y':
        print("Order cancelled")
        return
    
    # Step 4: Place order
    print("\n4. Placing order...")
    
    host = "https://clob.polymarket.com"
    key = os.getenv("PRIVATE_KEY")
    chain_id = 137
    
    try:
        # Initialize client
        client = ClobClient(host, key=key, chain_id=chain_id)
        api_creds = client.create_or_derive_api_creds()
        client.set_api_creds(api_creds)
        print("   ✓ Connected to Polymarket")
        
        # Create order
        order_args = OrderArgs(
            token_id=token_id,
            price=order_price,
            size=shares_to_buy,  # This is SHARES calculated from dollars/price
            side=BUY
        )
        
        signed_order = client.create_order(order_args)
        print("   ✓ Order signed")
        
        # Submit as GTC (will fill immediately at market price)
        resp = client.post_order(signed_order, OrderType.GTC)
        
        print("\n" + "="*70)
        print("✅ ORDER PLACED SUCCESSFULLY!")
        print("="*70)
        print(f"Order ID: {resp.get('orderID', 'N/A')}")
        print(f"Status: {resp.get('status', 'N/A')}")
        print("\nYour order should fill immediately at the best available price")
        print(f"You're betting on Jordan (up 2-0) to win")
        print("\n📱 Check at: https://polymarket.com/portfolio")
        
        # Step 5: Check if filled
        print("\nWant to check if the order filled? (y/n): ", end="")
        if input().lower() == 'y':
            check_recent_trades(client)
        
    except Exception as e:
        print(f"\n❌ Error: {e}")
        
        if "not enough balance" in str(e).lower():
            print("\nTroubleshooting:")
            print("1. Check USDC balance")
            print("2. Ensure contracts are approved")

def check_recent_trades(client):
    """Check recent trades to see if order filled"""
    try:
        trades = client.get_trades()
        if trades:
            print("\nYour recent trades:")
            for i, trade in enumerate(trades[:3]):
                side = trade.get('side', '')
                size = trade.get('size', 0)
                price = trade.get('price', 0)
                value = float(size) * float(price)
                print(f"{i+1}. {side}: {size} shares @ ${price} = ${value:.2f}")
    except:
        print("Could not fetch recent trades")

if __name__ == "__main__":
    place_jordan_market_buy()

