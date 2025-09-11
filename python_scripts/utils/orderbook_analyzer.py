#!/usr/bin/env python3
"""
Orderbook analysis utility for Polymarket CLOB
Provides precise fill simulations and market depth analysis

Usage:
    # Standalone analysis
    python orderbook_analyzer.py --token-id <token_id>
    
    # Or import for use in other scripts
    from orderbook_analyzer import OrderbookAnalyzer
"""

import argparse
import requests
import json
from decimal import Decimal, ROUND_DOWN
from typing import Dict, List, Tuple, Optional
from tabulate import tabulate
from datetime import datetime

class OrderbookAnalyzer:
    """Analyzes Polymarket CLOB orderbook for precise fill simulations"""
    
    def __init__(self):
        self.base_url = "https://clob.polymarket.com"
    
    def fetch_orderbook(self, token_id: str) -> Dict:
        """Fetch current orderbook for a token"""
        url = f"{self.base_url}/book"
        params = {"token_id": token_id}
        
        response = requests.get(url, params=params, timeout=10)
        if response.status_code != 200:
            raise ValueError(f"Failed to fetch orderbook: {response.status_code}")
        
        return response.json()
    
    def simulate_buy_fill(self, orderbook: Dict, dollar_amount: float, 
                         limit_price: Optional[float] = None) -> Dict:
        """
        Simulate a buy order fill through the orderbook
        Returns detailed fill information
        """
        asks = orderbook.get('asks', [])
        if not asks:
            return {
                'fillable': False,
                'reason': 'No sellers in orderbook',
                'filled_amount': 0,
                'shares_acquired': 0
            }
        
        # Sort asks by price (should already be sorted, but ensure)
        asks = sorted(asks, key=lambda x: float(x['price']))
        
        filled_amount = 0
        shares_acquired = 0
        fill_prices = []
        unfilled_amount = dollar_amount
        
        for ask in asks:
            price = float(ask['price'])
            size = float(ask['size'])
            
            # Skip if price exceeds limit
            if limit_price and price > limit_price:
                break
            
            # Calculate how much we can buy at this level
            cost_at_level = price * size
            
            if unfilled_amount >= cost_at_level:
                # Buy entire level
                shares_acquired += size
                filled_amount += cost_at_level
                unfilled_amount -= cost_at_level
                fill_prices.append({
                    'price': price,
                    'shares': size,
                    'cost': cost_at_level
                })
            else:
                # Partial fill at this level
                shares_at_price = unfilled_amount / price
                shares_acquired += shares_at_price
                filled_amount += unfilled_amount
                fill_prices.append({
                    'price': price,
                    'shares': shares_at_price,
                    'cost': unfilled_amount
                })
                unfilled_amount = 0
                break
        
        # Calculate weighted average price
        avg_price = filled_amount / shares_acquired if shares_acquired > 0 else 0
        
        return {
            'fillable': unfilled_amount == 0,
            'filled_amount': filled_amount,
            'unfilled_amount': unfilled_amount,
            'shares_acquired': shares_acquired,
            'average_price': avg_price,
            'worst_price': fill_prices[-1]['price'] if fill_prices else 0,
            'best_price': fill_prices[0]['price'] if fill_prices else 0,
            'fill_levels': len(fill_prices),
            'fill_details': fill_prices,
            'slippage': (avg_price - fill_prices[0]['price']) if fill_prices else 0
        }
    
    def simulate_sell_fill(self, orderbook: Dict, shares: float,
                          limit_price: Optional[float] = None) -> Dict:
        """
        Simulate a sell order fill through the orderbook
        Returns detailed fill information
        """
        bids = orderbook.get('bids', [])
        if not bids:
            return {
                'fillable': False,
                'reason': 'No buyers in orderbook',
                'proceeds': 0,
                'shares_sold': 0
            }
        
        # Sort bids by price descending (highest first)
        bids = sorted(bids, key=lambda x: float(x['price']), reverse=True)
        
        proceeds = 0
        shares_sold = 0
        fill_prices = []
        unfilled_shares = shares
        
        for bid in bids:
            price = float(bid['price'])
            size = float(bid['size'])
            
            # Skip if price below limit
            if limit_price and price < limit_price:
                break
            
            if unfilled_shares >= size:
                # Sell entire level
                shares_sold += size
                proceeds += price * size
                unfilled_shares -= size
                fill_prices.append({
                    'price': price,
                    'shares': size,
                    'proceeds': price * size
                })
            else:
                # Partial fill at this level
                shares_sold += unfilled_shares
                proceeds += price * unfilled_shares
                fill_prices.append({
                    'price': price,
                    'shares': unfilled_shares,
                    'proceeds': price * unfilled_shares
                })
                unfilled_shares = 0
                break
        
        # Calculate weighted average price
        avg_price = proceeds / shares_sold if shares_sold > 0 else 0
        
        return {
            'fillable': unfilled_shares == 0,
            'proceeds': proceeds,
            'shares_sold': shares_sold,
            'unfilled_shares': unfilled_shares,
            'average_price': avg_price,
            'worst_price': fill_prices[-1]['price'] if fill_prices else 0,
            'best_price': fill_prices[0]['price'] if fill_prices else 0,
            'fill_levels': len(fill_prices),
            'fill_details': fill_prices,
            'slippage': (fill_prices[0]['price'] - avg_price) if fill_prices else 0
        }
    
    def analyze_spread(self, orderbook: Dict) -> Dict:
        """Analyze bid-ask spread and market metrics"""
        bids = orderbook.get('bids', [])
        asks = orderbook.get('asks', [])
        
        if not bids or not asks:
            return {
                'has_liquidity': False,
                'best_bid': 0,
                'best_ask': 0,
                'spread': 0,
                'mid_price': 0
            }
        
        best_bid = float(bids[0]['price'])
        best_ask = float(asks[0]['price'])
        spread = best_ask - best_bid
        mid_price = (best_bid + best_ask) / 2
        
        # Calculate depth at various levels
        def calculate_depth(orders, levels=5):
            depth = []
            for i, order in enumerate(orders[:levels]):
                depth.append({
                    'level': i + 1,
                    'price': float(order['price']),
                    'size': float(order['size']),
                    'value': float(order['price']) * float(order['size'])
                })
            return depth
        
        return {
            'has_liquidity': True,
            'best_bid': best_bid,
            'best_ask': best_ask,
            'spread': spread,
            'spread_percent': (spread / mid_price) * 100,
            'mid_price': mid_price,
            'bid_depth': calculate_depth(bids),
            'ask_depth': calculate_depth(asks),
            'total_bid_value': sum(float(b['price']) * float(b['size']) for b in bids),
            'total_ask_value': sum(float(a['price']) * float(a['size']) for a in asks),
            'timestamp': orderbook.get('timestamp', datetime.now().isoformat())
        }
    
    def calculate_market_impact(self, orderbook: Dict, side: str, 
                               amount: float = None, shares: float = None) -> Dict:
        """Calculate the market impact of a hypothetical order"""
        if side == 'buy':
            if not amount:
                raise ValueError("Amount required for buy impact calculation")
            
            # Simulate at different sizes
            impacts = []
            for multiplier in [0.5, 1.0, 2.0, 5.0]:
                test_amount = amount * multiplier
                sim = self.simulate_buy_fill(orderbook, test_amount)
                if sim['fillable']:
                    impacts.append({
                        'amount': test_amount,
                        'avg_price': sim['average_price'],
                        'slippage': sim['slippage'],
                        'levels_consumed': sim['fill_levels']
                    })
            
            return {
                'side': 'buy',
                'base_amount': amount,
                'impact_curve': impacts
            }
        
        else:  # sell
            if not shares:
                raise ValueError("Shares required for sell impact calculation")
            
            impacts = []
            for multiplier in [0.5, 1.0, 2.0, 5.0]:
                test_shares = shares * multiplier
                sim = self.simulate_sell_fill(orderbook, test_shares)
                if sim['fillable']:
                    impacts.append({
                        'shares': test_shares,
                        'avg_price': sim['average_price'],
                        'slippage': sim['slippage'],
                        'levels_consumed': sim['fill_levels']
                    })
            
            return {
                'side': 'sell',
                'base_shares': shares,
                'impact_curve': impacts
            }
    
    def format_orderbook_display(self, orderbook: Dict, levels: int = 10) -> str:
        """Format orderbook for display"""
        asks = orderbook.get('asks', [])[:levels]
        bids = orderbook.get('bids', [])[:levels]
        
        # Reverse asks for traditional display (best at bottom)
        asks_display = []
        for ask in reversed(asks):
            price = float(ask['price'])
            size = float(ask['size'])
            asks_display.append({
                'Price': f"{price:.3f}",
                'Size': f"{size:.2f}",
                'Value': f"${price * size:.2f}",
                'Side': 'ASK'
            })
        
        # Add spread indicator
        if asks and bids:
            spread = float(asks[0]['price']) - float(bids[0]['price'])
            asks_display.append({
                'Price': '---',
                'Size': f"Spread: {spread:.3f}",
                'Value': f"{(spread/float(asks[0]['price']))*100:.2f}%",
                'Side': '---'
            })
        
        # Add bids
        bids_display = []
        for bid in bids:
            price = float(bid['price'])
            size = float(bid['size'])
            bids_display.append({
                'Price': f"{price:.3f}",
                'Size': f"{size:.2f}",
                'Value': f"${price * size:.2f}",
                'Side': 'BID'
            })
        
        all_rows = asks_display + bids_display
        return tabulate(all_rows, headers='keys', tablefmt='grid')

def main():
    parser = argparse.ArgumentParser(description='Analyze Polymarket orderbook')
    parser.add_argument('--token-id', required=True, help='Token ID to analyze')
    parser.add_argument('--simulate-buy', type=float, help='Simulate buying $X')
    parser.add_argument('--simulate-sell', type=float, help='Simulate selling X shares')
    parser.add_argument('--limit-price', type=float, help='Limit price for simulation')
    parser.add_argument('--levels', type=int, default=10, help='Orderbook levels to display')
    parser.add_argument('--json', action='store_true', help='Output as JSON')
    
    args = parser.parse_args()
    
    analyzer = OrderbookAnalyzer()
    
    try:
        # Fetch orderbook
        orderbook = analyzer.fetch_orderbook(args.token_id)
        
        if args.json:
            results = {'orderbook': orderbook}
        else:
            print(f"\n📊 ORDERBOOK for Token {args.token_id}")
            print("=" * 60)
            print(analyzer.format_orderbook_display(orderbook, args.levels))
        
        # Analyze spread
        spread_info = analyzer.analyze_spread(orderbook)
        if not args.json:
            print("\n📈 MARKET METRICS:")
            print(f"  Mid Price: {spread_info['mid_price']:.3f}")
            print(f"  Spread: {spread_info['spread']:.3f} ({spread_info.get('spread_percent', 0):.2f}%)")
            print(f"  Total Bid Value: ${spread_info['total_bid_value']:.2f}")
            print(f"  Total Ask Value: ${spread_info['total_ask_value']:.2f}")
        
        # Simulate buy if requested
        if args.simulate_buy:
            sim = analyzer.simulate_buy_fill(orderbook, args.simulate_buy, args.limit_price)
            
            if args.json:
                results['buy_simulation'] = sim
            else:
                print(f"\n💰 BUY SIMULATION: ${args.simulate_buy:.2f}")
                print("=" * 60)
                if sim['fillable']:
                    print("✅ FULLY FILLABLE")
                    print(f"  Shares acquired: {sim['shares_acquired']:.4f}")
                    print(f"  Average price: {sim['average_price']:.3f}")
                    print(f"  Best price: {sim['best_price']:.3f}")
                    print(f"  Worst price: {sim['worst_price']:.3f}")
                    print(f"  Slippage: {sim['slippage']:.3f}")
                    print(f"  Levels consumed: {sim['fill_levels']}")
                    
                    if sim['fill_details'] and len(sim['fill_details']) <= 5:
                        print("\n  Fill breakdown:")
                        for fill in sim['fill_details']:
                            print(f"    {fill['shares']:.2f} shares @ {fill['price']:.3f} = ${fill['cost']:.2f}")
                else:
                    print("⚠️  PARTIALLY FILLABLE")
                    print(f"  Can fill: ${sim['filled_amount']:.2f} of ${args.simulate_buy:.2f}")
                    print(f"  Unfilled: ${sim['unfilled_amount']:.2f}")
                    if args.limit_price:
                        print(f"  (Limited by price {args.limit_price:.3f})")
        
        # Simulate sell if requested
        if args.simulate_sell:
            sim = analyzer.simulate_sell_fill(orderbook, args.simulate_sell, args.limit_price)
            
            if args.json:
                results['sell_simulation'] = sim
            else:
                print(f"\n💸 SELL SIMULATION: {args.simulate_sell:.2f} shares")
                print("=" * 60)
                if sim['fillable']:
                    print("✅ FULLY FILLABLE")
                    print(f"  Proceeds: ${sim['proceeds']:.2f}")
                    print(f"  Average price: {sim['average_price']:.3f}")
                    print(f"  Best price: {sim['best_price']:.3f}")
                    print(f"  Worst price: {sim['worst_price']:.3f}")
                    print(f"  Slippage: {sim['slippage']:.3f}")
                    print(f"  Levels consumed: {sim['fill_levels']}")
                    
                    if sim['fill_details'] and len(sim['fill_details']) <= 5:
                        print(f"\n  Fill breakdown:")
                        for fill in sim['fill_details']:
                            print(f"    {fill['shares']:.2f} shares @ {fill['price']:.3f} = ${fill['proceeds']:.2f}")
                elif sim.get('reason'):
                    print(f"❌ CANNOT FILL: {sim['reason']}")
                else:
                    print("⚠️  PARTIALLY FILLABLE")
                    print(f"  Can sell: {sim['shares_sold']:.2f} of {args.simulate_sell:.2f} shares")
                    print(f"  Unfilled: {sim['unfilled_shares']:.2f} shares")
                    if args.limit_price:
                        print(f"  (Limited by price {args.limit_price:.3f})")
        
        if args.json:
            print(json.dumps(results, indent=2))
    
    except Exception as e:
        print(f"❌ Error: {e}")
        return 1
    
    return 0

if __name__ == "__main__":
    exit(main())

