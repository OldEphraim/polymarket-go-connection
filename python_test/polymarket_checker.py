#!/usr/bin/env python3
"""
Check all your Polymarket positions, orders, and current market prices
"""

import os
import json
import requests
from py_clob_client.client import ClobClient
from dotenv import load_dotenv
from web3 import Web3

load_dotenv('../.env')

def check_onchain_balances():
    """Check your actual CTF token balances on-chain"""
    print("\n" + "="*70)
    print("ON-CHAIN CTF TOKEN BALANCES")
    print("="*70)
    
    web3 = Web3(Web3.HTTPProvider("https://polygon-rpc.com"))
    CTF_ADDRESS = "0x4D97DCd97eC945f40cF65F87097ACe5EA0476045"
    YOUR_ADDRESS = os.getenv("YOUR_ADDRESS", "0x1A106d01540FB3a3B631226Bab98DA6959838c7b")
    
    # Known tokens you might own
    known_tokens = {
        "Ukraine Yes": "69295430172447163159793296100162053628203745168718129321397048391227518839454",
        "Senegal Yes": "88309419917004099428094188172685752533903668411511547229384883756825978807950",
        "Senegal No": "49517465175841448179118018066053997803004395603964872588579842659893215902615"
    }
    
    ABI = [{
        "inputs": [
            {"internalType": "address", "name": "account", "type": "address"},
            {"internalType": "uint256", "name": "id", "type": "uint256"}
        ],
        "name": "balanceOf",
        "outputs": [{"internalType": "uint256", "name": "", "type": "uint256"}],
        "stateMutability": "view",
        "type": "function"
    }]
    
    ctf = web3.eth.contract(address=CTF_ADDRESS, abi=ABI)
    
    print(f"Checking wallet: {YOUR_ADDRESS}")
    print("-"*70)
    
    total_positions = 0
    for name, token_id in known_tokens.items():
        try:
            balance = ctf.functions.balanceOf(YOUR_ADDRESS, int(token_id)).call()
            shares = balance / 1e6  # CTF tokens have 6 decimals
            
            if shares > 0:
                print(f"\n✓ {name}: {shares:.2f} shares")
                print(f"  Token ID: {token_id}")
                total_positions += 1
        except Exception as e:
            print(f"\n❌ Error checking {name}: {e}")
    
    if total_positions == 0:
        print("\nNo CTF tokens found in wallet")
    
    return total_positions

def get_polymarket_data():
    """Get all your Polymarket data via API"""
    print("\n" + "="*70)
    print("POLYMARKET API DATA")
    print("="*70)
    
    host = "https://clob.polymarket.com"
    key = os.getenv("PRIVATE_KEY")
    chain_id = 137
    
    if not key:
        print("❌ PRIVATE_KEY not found")
        return
    
    try:
        client = ClobClient(host, key=key, chain_id=chain_id)
        api_creds = client.create_or_derive_api_creds()
        client.set_api_creds(api_creds)
        print("✓ Connected to Polymarket API")
        
        # 1. Get Balance
        print("\n" + "-"*70)
        print("USDC BALANCE:")
        print("-"*70)
        try:
            balance_info = client.get_balance_allowance()
            print(f"Balance: ${balance_info.get('balance', 0)}")
            print(f"Allowance: ${balance_info.get('allowance', 0)}")
        except Exception as e:
            print(f"Could not fetch balance: {e}")
        
        # 2. Get Open Orders
        print("\n" + "-"*70)
        print("OPEN ORDERS:")
        print("-"*70)
        try:
            orders = client.get_orders()
            if orders:
                print(f"Found {len(orders)} open order(s)\n")
                
                for i, order in enumerate(orders):
                    # Show raw structure for debugging
                    if i == 0:
                        print(f"DEBUG - Available fields: {list(order.keys())}\n")
                    
                    # Extract fields with multiple fallbacks
                    side = order.get('side', 'UNKNOWN')
                    price = order.get('price', 0)
                    
                    # Try different field names for size
                    size = (order.get('size') or 
                           order.get('original_size') or 
                           order.get('unfilled_size') or
                           order.get('remaining_size') or
                           'Unknown')
                    
                    # Try different field names for ID
                    order_id = (order.get('orderID') or 
                               order.get('id') or 
                               order.get('order_id') or 
                               'Unknown')
                    
                    # Try to get market info
                    market = order.get('market') or order.get('asset_id') or ''
                    
                    print(f"Order #{i+1}:")
                    print(f"  Type: {side}")
                    print(f"  Price: ${price:.3f} ({price*100:.1f}¢)")
                    print(f"  Size: {size}")
                    print(f"  Order ID: {order_id}")
                    if market:
                        print(f"  Market: {market}")
                    print()
            else:
                print("No open orders")
        except Exception as e:
            print(f"Error fetching orders: {e}")
        
        # 3. Get Recent Trades
        print("\n" + "-"*70)
        print("RECENT TRADES (filled orders):")
        print("-"*70)
        try:
            trades = client.get_trades()
            if trades:
                print(f"Found {len(trades)} trade(s)\n")
                
                # Show last 10 trades
                for i, trade in enumerate(trades[:10]):
                    side = trade.get('side', 'UNKNOWN')
                    size = trade.get('size', 0)
                    price = trade.get('price', 0)
                    
                    # Calculate trade value
                    value = float(size) * float(price) if size and price else 0
                    
                    print(f"Trade #{i+1}:")
                    print(f"  {side}: {size} shares @ ${price:.3f}")
                    print(f"  Total value: ${value:.2f}")
                    
                    # Show timestamp if available
                    timestamp = trade.get('timestamp') or trade.get('created_at')
                    if timestamp:
                        print(f"  Time: {timestamp}")
                    print()
            else:
                print("No recent trades")
        except Exception as e:
            print(f"Error fetching trades: {e}")
        
        # 4. Try to get positions (this method might not exist)
        print("\n" + "-"*70)
        print("POSITIONS (if available):")
        print("-"*70)
        try:
            # Try different possible method names
            positions = None
            try:
                positions = client.get_positions()
            except:
                try:
                    positions = client.get_portfolio()
                except:
                    pass
            
            if positions:
                print(f"Found {len(positions)} position(s)")
                for pos in positions:
                    print(f"  {pos}")
            else:
                print("Positions method not available - check on-chain balances above")
        except Exception as e:
            print(f"Could not fetch positions via API: {e}")
            print("Check the on-chain balances section for actual holdings")
        
    except Exception as e:
        print(f"Error connecting to API: {e}")

def check_market_prices():
    """Check current market prices for your markets"""
    print("\n" + "="*70)
    print("CURRENT MARKET PRICES")
    print("="*70)
    
    markets = {
        "Ukraine (Sept 9 game)": {
            "slug": "will-ukraine-win-on-2025-09-09",
            "your_position": "5 shares @ 61¢",
            "your_sell_order": "15¢"
        },
        "Senegal vs DRC": {
            "slug": "caf-cdr-sen-2025-09-09-sen",
            "your_position": "None yet",
            "your_buy_order": "97.1¢"
        }
    }
    
    for name, info in markets.items():
        print(f"\n{name}:")
        print(f"  Your position: {info['your_position']}")
        print(f"  Your order: {info.get('your_buy_order') or info.get('your_sell_order')}")
        
        # Try to fetch current price
        try:
            url = f"https://gamma-api.polymarket.com/markets/slug/{info['slug']}"
            response = requests.get(url, timeout=5)
            
            if response.status_code == 200:
                data = response.json()
                
                # Parse prices
                if 'outcomePrices' in data:
                    prices_str = data['outcomePrices']
                    if prices_str.startswith('['):
                        prices = json.loads(prices_str)
                        print(f"  Current prices: Yes={float(prices[0])*100:.1f}¢, No={float(prices[1])*100:.1f}¢")
                    
                print(f"  Market active: {data.get('active', 'Unknown')}")
        except:
            print(f"  Could not fetch current price")

def main():
    """Run all checks"""
    print("="*70)
    print("COMPLETE POLYMARKET PORTFOLIO CHECK")
    print("="*70)
    
    # Check on-chain balances
    check_onchain_balances()
    
    # Check Polymarket API data
    get_polymarket_data()
    
    # Check current market prices
    check_market_prices()
    
    print("\n" + "="*70)
    print("SUMMARY")
    print("="*70)
    print("\n📊 What this means:")
    print("1. On-chain balances = shares you actually own")
    print("2. Open orders = orders waiting to be filled")
    print("3. Recent trades = completed buys/sells")
    print("\nIf Senegal moved to 99¢, your buy order at 97.1¢ won't fill")
    print("unless the price drops back down. You could:")
    print("- Cancel and place a new order at 99¢")
    print("- Wait for the price to drop")
    print("- Place a market order (buy at any price)")

if __name__ == "__main__":
    main()

