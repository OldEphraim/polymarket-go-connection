#!/usr/bin/env python3
"""
Main trading interface for Polymarket with enhanced dry-run using real orderbook data
Handles both BUY and SELL orders with various order types

Usage:
    python place_trade.py --slug <market-slug> --side <buy/sell> --amount <dollars> [options]
    
Examples:
    # Buy $5 worth at market price
    python place_trade.py --slug "will-trump-win-2024" --side buy --amount 5
    
    # Dry run with orderbook analysis
    python place_trade.py --slug "will-trump-win-2024" --side buy --amount 100 --dry-run
    
    # Sell 10 shares at limit price
    python place_trade.py --slug "will-trump-win-2024" --side sell --shares 10 --price 0.75
"""

import argparse
import os
import sys
import json
import requests
from decimal import Decimal, ROUND_DOWN
from py_clob_client.client import ClobClient
from py_clob_client.clob_types import OrderArgs, OrderType
from py_clob_client.order_builder.constants import BUY, SELL
from dotenv import load_dotenv
from web3 import Web3
from tabulate import tabulate

# Import our orderbook analyzer
sys.path.append(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from utils.orderbook_analyzer import OrderbookAnalyzer

load_dotenv()

class PolymarketTrader:
    """Main trading class for Polymarket operations"""
    
    def __init__(self):
        self.host = "https://clob.polymarket.com"
        self.key = os.getenv("PRIVATE_KEY")
        self.chain_id = 137
        self.address = os.getenv("WALLET_ADDRESS", "0x1A106d01540FB3a3B631226Bab98DA6959838c7b")
        
        if not self.key:
            raise ValueError("PRIVATE_KEY not found in .env file")
        
        # Initialize client
        self.client = ClobClient(self.host, key=self.key, chain_id=self.chain_id)
        api_creds = self.client.create_or_derive_api_creds()
        self.client.set_api_creds(api_creds)
        
        # Initialize orderbook analyzer
        self.orderbook_analyzer = OrderbookAnalyzer()
    
    def get_market_info(self, slug):
        """Fetch market information and token IDs"""
        url = f"https://gamma-api.polymarket.com/markets/slug/{slug}"
        response = requests.get(url, timeout=5)
        
        if response.status_code != 200:
            raise ValueError(f"Market not found: {slug}")
        
        data = response.json()
        
        # Parse token data
        token_ids = json.loads(data.get('clobTokenIds', '[]'))
        outcomes = json.loads(data.get('outcomes', '[]'))
        prices = json.loads(data.get('outcomePrices', '[]'))
        
        if not token_ids:
            raise ValueError(f"No token IDs found for market: {slug}")
        
        # Parse volume and liquidity - they might be strings
        volume = data.get('volume', 0)
        liquidity = data.get('liquidity', 0)
        
        # Convert to float if they're strings
        if isinstance(volume, str):
            volume = float(volume) if volume else 0
        if isinstance(liquidity, str):
            liquidity = float(liquidity) if liquidity else 0
        
        return {
            'question': data.get('question', 'Unknown'),
            'active': data.get('active', False),
            'closed': data.get('closed', False),
            'volume': volume,
            'liquidity': liquidity,
            'tokens': [
                {
                    'outcome': outcomes[i] if i < len(outcomes) else f"Outcome {i+1}",
                    'token_id': token_ids[i],
                    'price': float(prices[i]) if i < len(prices) else 0.5
                }
                for i in range(len(token_ids))
            ]
        }
    
    def get_share_balance(self, token_id):
        """Check how many shares of a token the user owns"""
        web3 = Web3(Web3.HTTPProvider("https://polygon-rpc.com"))
        CTF_ADDRESS = "0x4D97DCd97eC945f40cF65F87097ACe5EA0476045"
        
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
        balance = ctf.functions.balanceOf(self.address, int(token_id)).call()
        return balance / 1e6  # CTF tokens have 6 decimals
    
    def perform_dry_run(self, token_id, side, amount=None, shares=None, price=None):
        """
        Perform enhanced dry run with real orderbook data
        Returns detailed simulation results
        """
        # Fetch current orderbook
        orderbook = self.orderbook_analyzer.fetch_orderbook(token_id)
        spread_info = self.orderbook_analyzer.analyze_spread(orderbook)
        
        results = {
            'market_state': spread_info,
            'simulation': None,
            'risk_analysis': {},
            'recommendations': []
        }
        
        if side == 'buy':
            # Simulate buy
            sim = self.orderbook_analyzer.simulate_buy_fill(orderbook, amount, price)
            results['simulation'] = sim
            
            # Risk analysis
            if sim['fillable']:
                results['risk_analysis'] = {
                    'immediate_loss_if_sold': amount - (sim['shares_acquired'] * spread_info['best_bid']),
                    'breakeven_price': amount / sim['shares_acquired'] if sim['shares_acquired'] > 0 else 0,
                    'profit_at_midpoint': (sim['shares_acquired'] * spread_info['mid_price']) - amount,
                    'max_profit_if_wins': (sim['shares_acquired'] * 1.0) - amount,
                    'max_loss': amount
                }
                
                # Recommendations
                if sim['slippage'] > 0.01:
                    results['recommendations'].append(f"High slippage ({sim['slippage']:.3f}). Consider smaller order or limit price.")
                if sim['fill_levels'] > 3:
                    results['recommendations'].append(f"Order consumes {sim['fill_levels']} price levels. Market impact likely.")
                if spread_info['spread_percent'] > 2:
                    results['recommendations'].append(f"Wide spread ({spread_info['spread_percent']:.1f}%). Consider limit order.")
            else:
                results['recommendations'].append("Order cannot be fully filled. Reduce size or increase limit price.")
        
        else:  # sell
            # Simulate sell
            sim = self.orderbook_analyzer.simulate_sell_fill(orderbook, shares, price)
            results['simulation'] = sim
            
            # Risk analysis
            if sim['fillable']:
                current_value_at_ask = shares * spread_info['best_ask']
                results['risk_analysis'] = {
                    'immediate_proceeds': sim['proceeds'],
                    'value_if_bought_back': current_value_at_ask,
                    'round_trip_cost': current_value_at_ask - sim['proceeds'],
                    'effective_spread_cost': (current_value_at_ask - sim['proceeds']) / current_value_at_ask * 100
                }
                
                # Recommendations
                if sim['slippage'] > 0.01:
                    results['recommendations'].append(f"High slippage ({sim['slippage']:.3f}). Consider smaller order or limit price.")
                if sim['fill_levels'] > 3:
                    results['recommendations'].append(f"Order consumes {sim['fill_levels']} price levels. Market impact likely.")
        
        return results
    
    def place_buy_order(self, token_id, dollar_amount, max_price=None, order_type="GTC"):
        """Place a buy order for a specific dollar amount"""
        
        # If no max_price specified, use 0.99 (market order behavior)
        if max_price is None:
            max_price = 0.99
        
        # Calculate shares with proper precision
        shares = round(dollar_amount / max_price, 4)  # 4 decimal precision for size
        
        order_args = OrderArgs(
            token_id=token_id,
            price=max_price,
            size=shares,
            side=BUY
        )
        
        signed_order = self.client.create_order(order_args)
        
        # Map string order type to OrderType enum
        order_type_map = {
            "GTC": OrderType.GTC,
            "FOK": OrderType.FOK,
            "IOC": OrderType.IOC
        }
        
        return self.client.post_order(signed_order, order_type_map.get(order_type, OrderType.GTC))
    
    def place_sell_order(self, token_id, shares, min_price=None, order_type="GTC"):
        """Place a sell order for a specific number of shares"""
        
        # If no min_price specified, use 0.01 (market order behavior)
        if min_price is None:
            min_price = 0.01
        
        # Round shares to 4 decimals
        shares = round(shares, 4)
        
        order_args = OrderArgs(
            token_id=token_id,
            price=min_price,
            size=shares,
            side=SELL
        )
        
        signed_order = self.client.create_order(order_args)
        
        # Map string order type to OrderType enum
        order_type_map = {
            "GTC": OrderType.GTC,
            "FOK": OrderType.FOK,
            "IOC": OrderType.IOC
        }
        
        return self.client.post_order(signed_order, order_type_map.get(order_type, OrderType.GTC))

def format_dry_run_report(market, token, dry_run_results):
    """Format comprehensive dry run report"""
    output = []
    
    # Market overview
    output.append("\n" + "="*70)
    output.append("📊 MARKET ANALYSIS REPORT")
    output.append("="*70)
    
    output.append(f"\nMarket: {market['question']}")
    output.append(f"Outcome: {token['outcome']}")
    output.append(f"Volume: ${market.get('volume', 0):,.0f}")
    output.append(f"Liquidity: ${market.get('liquidity', 0):,.0f}")
    
    # Current market state
    state = dry_run_results['market_state']
    if state.get('has_two_sided_liquidity'):
        output.append(f"\n📈 CURRENT MARKET STATE:")
        output.append(f"  Best Bid: {state['best_bid']:.3f} ({state['best_bid']*100:.1f}¢)")
        output.append(f"  Best Ask: {state['best_ask']:.3f} ({state['best_ask']*100:.1f}¢)")
        output.append(f"  Mid Price: {state['mid_price']:.3f} ({state['mid_price']*100:.1f}¢)")
        output.append(f"  Spread: {state['spread']:.3f} ({state['spread_percent']:.2f}%)")
    elif state.get('warning'):
        output.append(f"\n⚠️  MARKET WARNING: {state['warning']}")
        if state.get('best_bid'):
            output.append(f"  Best Bid: {state['best_bid']:.3f} ({state['best_bid']*100:.1f}¢)")
        if state.get('best_ask'):
            output.append(f"  Best Ask: {state['best_ask']:.3f} ({state['best_ask']*100:.1f}¢)")
    
    # Order simulation
    sim = dry_run_results['simulation']
    output.append(f"\n💰 ORDER SIMULATION:")
    
    if sim.get('reason'):
        output.append(f"❌ CANNOT EXECUTE: {sim['reason']}")
    elif sim['fillable']:
        output.append("✅ Order is FULLY FILLABLE")
        
        if 'shares_acquired' in sim:  # Buy order
            output.append(f"  Investment: ${sim['filled_amount']:.2f}")
            output.append(f"  Shares acquired: {sim['shares_acquired']:.4f}")
            output.append(f"  Average fill price: {sim['average_price']:.3f} ({sim['average_price']*100:.1f}¢)")
            output.append(f"  Price range: {sim['best_price']:.3f} to {sim['worst_price']:.3f}")
            output.append(f"  Slippage: {sim['slippage']:.3f} ({sim['slippage']*100:.1f}¢)")
            output.append(f"  Orderbook levels consumed: {sim['fill_levels']}")
        else:  # Sell order
            output.append(f"  Shares to sell: {sim['shares_sold']:.4f}")
            output.append(f"  Expected proceeds: ${sim['proceeds']:.2f}")
            output.append(f"  Average fill price: {sim['average_price']:.3f} ({sim['average_price']*100:.1f}¢)")
            output.append(f"  Price range: {sim['best_price']:.3f} to {sim['worst_price']:.3f}")
            output.append(f"  Slippage: {sim['slippage']:.3f} ({sim['slippage']*100:.1f}¢)")
            output.append(f"  Orderbook levels consumed: {sim['fill_levels']}")
        
        # Fill breakdown (if not too many levels)
        if sim.get('fill_details') and sim['fill_levels'] <= 5:
            output.append(f"\n  Fill Breakdown:")
            for i, fill in enumerate(sim['fill_details'], 1):
                if 'cost' in fill:  # Buy
                    output.append(f"    Level {i}: {fill['shares']:.2f} shares @ {fill['price']:.3f} = ${fill['cost']:.2f}")
                else:  # Sell
                    output.append(f"    Level {i}: {fill['shares']:.2f} shares @ {fill['price']:.3f} = ${fill['proceeds']:.2f}")
    else:
        output.append("⚠️  Order is PARTIALLY FILLABLE")
        if 'filled_amount' in sim:  # Buy
            output.append(f"  Can fill: ${sim['filled_amount']:.2f} of requested amount")
            output.append(f"  Unfilled: ${sim['unfilled_amount']:.2f}")
        else:  # Sell
            output.append(f"  Can sell: {sim['shares_sold']:.2f} shares")
            output.append(f"  Unfilled: {sim['unfilled_shares']:.2f} shares")
    
    # Risk analysis
    risk = dry_run_results['risk_analysis']
    if risk:
        output.append(f"\n⚠️  RISK ANALYSIS:")
        if 'warning' in risk:
            output.append(f"  {risk['warning']}")
        if 'immediate_loss_if_sold' in risk and risk['immediate_loss_if_sold'] is not None:  # Buy order
            output.append(f"  Immediate loss if sold: ${risk['immediate_loss_if_sold']:.2f}")
            output.append(f"  Breakeven price: {risk['breakeven_price']:.3f} ({risk['breakeven_price']*100:.1f}¢)")
            if risk.get('profit_at_midpoint') is not None:
                output.append(f"  Profit at mid-price: ${risk['profit_at_midpoint']:.2f}")
            output.append(f"  Max profit (if wins): ${risk['max_profit_if_wins']:.2f}")
            output.append(f"  Max loss: ${risk['max_loss']:.2f}")
            
            # Probability assessment
            if sim.get('fillable') and sim.get('average_price'):
                implied_prob = sim['average_price'] * 100
                breakeven_prob = risk['breakeven_price'] * 100
                output.append(f"\n  Probability Assessment:")
                output.append(f"    Market implies {implied_prob:.1f}% chance")
                output.append(f"    Need {breakeven_prob:.1f}% to break even")
                
                if breakeven_prob > implied_prob:
                    output.append(f"    ⚠️  Negative expected value at market odds")
        elif 'immediate_proceeds' in risk:  # Sell order
            output.append(f"  Immediate proceeds: ${risk['immediate_proceeds']:.2f}")
            if 'value_if_bought_back' in risk:
                output.append(f"  Cost to buy back: ${risk['value_if_bought_back']:.2f}")
                output.append(f"  Round-trip cost: ${risk['round_trip_cost']:.2f}")
                output.append(f"  Effective spread cost: {risk['effective_spread_cost']:.1f}%")
    
    # Recommendations
    if dry_run_results['recommendations']:
        output.append(f"\n💡 RECOMMENDATIONS:")
        for rec in dry_run_results['recommendations']:
            output.append(f"  • {rec}")
    
    output.append("\n" + "="*70)
    
    return "\n".join(output)

def main():
    parser = argparse.ArgumentParser(description='Place trades on Polymarket')
    parser.add_argument('--slug', required=True, help='Market slug (e.g., "will-trump-win-2024")')
    parser.add_argument('--side', required=True, choices=['buy', 'sell'], help='Buy or sell')
    parser.add_argument('--outcome', required=True, type=int, help='Outcome index (0 for first/Yes, 1 for second/No)')
    parser.add_argument('--amount', type=float, help='Dollar amount for buy orders')
    parser.add_argument('--shares', type=float, help='Number of shares for sell orders')
    parser.add_argument('--price', type=float, help='Limit price (max for buy, min for sell)')
    parser.add_argument('--order-type', default='GTC', choices=['GTC', 'FOK', 'IOC'], 
                       help='Order type: GTC (default), FOK (fill-or-kill), IOC (immediate-or-cancel)')
    parser.add_argument('--dry-run', action='store_true', help='Show detailed analysis without placing order')
    parser.add_argument('--json', action='store_true', help='Output dry run as JSON')
    
    args = parser.parse_args()
    
    # Validate arguments
    if args.side == 'buy' and not args.amount:
        parser.error("--amount is required for buy orders")
    if args.side == 'sell' and not args.shares:
        parser.error("--shares is required for sell orders")
    
    try:
        trader = PolymarketTrader()
        
        # Get market info
        print(f"\nFetching market: {args.slug}")
        market = trader.get_market_info(args.slug)
        
        print(f"Market: {market['question']}")
        print(f"Active: {market['active']}, Closed: {market['closed']}")
        
        # Select outcome
        if args.outcome >= len(market['tokens']):
            print(f"Error: Outcome {args.outcome} not found. Available outcomes:")
            for i, token in enumerate(market['tokens']):
                print(f"  {i}: {token['outcome']} (current price: {token['price']*100:.1f}¢)")
            sys.exit(1)
        
        token = market['tokens'][args.outcome]
        print(f"\nSelected outcome: {token['outcome']}")
        print(f"Current price: {token['price']*100:.1f}¢")
        print(f"Token ID: {token['token_id']}")
        
        if args.dry_run:
            # Perform enhanced dry run with orderbook data
            print("\n🔍 Fetching live orderbook data...")
            
            if args.side == 'buy':
                dry_run_results = trader.perform_dry_run(
                    token['token_id'], 
                    'buy', 
                    amount=args.amount,
                    price=args.price
                )
            else:
                dry_run_results = trader.perform_dry_run(
                    token['token_id'],
                    'sell',
                    shares=args.shares,
                    price=args.price
                )
            
            if args.json:
                # Output as JSON for programmatic use
                output = {
                    'market': market,
                    'selected_token': token,
                    'order_params': {
                        'side': args.side,
                        'amount': args.amount,
                        'shares': args.shares,
                        'price': args.price,
                        'order_type': args.order_type
                    },
                    'simulation': dry_run_results
                }
                print(json.dumps(output, indent=2))
            else:
                # Format human-readable report
                report = format_dry_run_report(market, token, dry_run_results)
                print(report)
                print("\n[DRY RUN COMPLETE - No order placed]")
            
            return
        
        # Regular order placement (non dry-run)
        if args.side == 'buy':
            # Buy order
            price = args.price if args.price else 0.99  # Default to market order
            shares_expected = args.amount / price
            
            print(f"\n📊 BUY ORDER DETAILS:")
            print(f"  Amount: ${args.amount:.2f}")
            print(f"  Max price: {price*100:.1f}¢")
            print(f"  Expected shares: {shares_expected:.4f}")
            print(f"  Order type: {args.order_type}")
            
            confirm = input("\n⚠️  Place this order? (y/n): ")
            if confirm.lower() != 'y':
                print("Order cancelled")
                return
            
            resp = trader.place_buy_order(
                token['token_id'],
                args.amount,
                price,
                args.order_type
            )
            
        else:
            # Sell order
            # Check current balance
            balance = trader.get_share_balance(token['token_id'])
            print(f"\nYou own: {balance:.4f} shares")
            
            if args.shares > balance:
                print(f"Error: You only have {balance:.4f} shares, cannot sell {args.shares}")
                sys.exit(1)
            
            price = args.price if args.price else 0.01  # Default to market order
            expected_return = args.shares * price
            
            print(f"\n📊 SELL ORDER DETAILS:")
            print(f"  Shares: {args.shares:.4f}")
            print(f"  Min price: {price*100:.1f}¢")
            print(f"  Expected return: ${expected_return:.2f}")
            print(f"  Order type: {args.order_type}")
            
            confirm = input("\n⚠️  Place this order? (y/n): ")
            if confirm.lower() != 'y':
                print("Order cancelled")
                return
            
            resp = trader.place_sell_order(
                token['token_id'],
                args.shares,
                price,
                args.order_type
            )
        
        # Show result
        print("\n✅ ORDER PLACED SUCCESSFULLY!")
        print(f"Order ID: {resp.get('orderID', 'N/A')}")
        print(f"Status: {resp.get('status', 'N/A')}")
        print("\n📱 Check at: https://polymarket.com/portfolio")
        
    except Exception as e:
        print(f"\n❌ Error: {e}")
        
        # Provide helpful error messages
        if "not enough balance" in str(e).lower():
            print("\nPossible issues:")
            print("1. Insufficient USDC balance (for buys)")
            print("2. Insufficient share balance (for sells)")
            print("3. Contracts not approved - run complete_approvals.py")
        elif "couldn't be fully filled" in str(e).lower():
            print("\nInsufficient liquidity for FOK order.")
            print("Try using GTC or reducing order size.")

if __name__ == "__main__":
    main()

