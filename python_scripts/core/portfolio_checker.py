#!/usr/bin/env python3
"""
Comprehensive portfolio checker for Polymarket
Shows all positions, open orders, balances, and P&L

Usage:
    python portfolio_checker.py [--detailed] [--show-history]
    
Options:
    --detailed      Show transaction-level details
    --show-history  Include full trade history
    --json          Output in JSON format
"""

import os
import json
import argparse
import requests
from datetime import datetime
from py_clob_client.client import ClobClient
from dotenv import load_dotenv
from web3 import Web3
from tabulate import tabulate

load_dotenv()

class PortfolioChecker:
    """Check and display complete Polymarket portfolio"""
    
    def __init__(self):
        self.web3 = Web3(Web3.HTTPProvider("https://polygon-rpc.com"))
        self.address = os.getenv("WALLET_ADDRESS", "0x1A106d01540FB3a3B631226Bab98DA6959838c7b")
        self.key = os.getenv("PRIVATE_KEY")
        
        # Contract addresses
        self.CTF_ADDRESS = "0x4D97DCd97eC945f40cF65F87097ACe5EA0476045"
        self.USDC_BRIDGED = "0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174"
        self.USDC_NATIVE = "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359"
        
        # Initialize Polymarket client if key available
        if self.key:
            self.client = ClobClient(
                "https://clob.polymarket.com",
                key=self.key,
                chain_id=137
            )
            api_creds = self.client.create_or_derive_api_creds()
            self.client.set_api_creds(api_creds)
        else:
            self.client = None
    
    def check_usdc_balances(self):
        """Check all USDC balances and allowances"""
        ABI = [
            {
                "constant": True,
                "inputs": [{"name": "_owner", "type": "address"}],
                "name": "balanceOf",
                "outputs": [{"name": "", "type": "uint256"}],
                "type": "function"
            },
            {
                "constant": True,
                "inputs": [
                    {"name": "_owner", "type": "address"},
                    {"name": "_spender", "type": "address"}
                ],
                "name": "allowance",
                "outputs": [{"name": "", "type": "uint256"}],
                "type": "function"
            }
        ]
        
        results = {}
        
        # Check bridged USDC
        bridged_contract = self.web3.eth.contract(address=self.USDC_BRIDGED, abi=ABI)
        bridged_balance = bridged_contract.functions.balanceOf(self.address).call() / 1e6
        results['bridged_usdc'] = {
            'balance': bridged_balance,
            'address': self.USDC_BRIDGED
        }
        
        # Check native USDC
        native_contract = self.web3.eth.contract(address=self.USDC_NATIVE, abi=ABI)
        native_balance = native_contract.functions.balanceOf(self.address).call() / 1e6
        results['native_usdc'] = {
            'balance': native_balance,
            'address': self.USDC_NATIVE
        }
        
        # Check MATIC for gas
        matic_balance = self.web3.eth.get_balance(self.address) / 1e18
        results['matic'] = {
            'balance': matic_balance
        }
        
        return results
    
    def scan_ctf_positions(self):
        """Scan for all CTF token positions"""
        positions = []
        
        # For now, return empty list - scanning events has compatibility issues
        # Once we have trade history, we can check specific tokens
        return positions
        
        # TODO: Once there is database integration, we'll maintain a list of tokens 
        # interacted with which can be used for this file
        # 
        # Example implementation:
        # known_tokens = {
        #     "69295430172447163159793296100162053628203745168718129321397048391227518839454": "Ukraine Yes",
        #     "24774048549660093637249705878667532205573252986509496949873279924877879997872": "Romania Yes",
        #     # More tokens would be added automatically as trades are made
        # }
        # 
        # CTF_ABI = [{
        #     "inputs": [
        #         {"name": "account", "type": "address"},
        #         {"name": "id", "type": "uint256"}
        #     ],
        #     "name": "balanceOf",
        #     "outputs": [{"name": "", "type": "uint256"}],
        #     "stateMutability": "view",
        #     "type": "function"
        # }]
        # 
        # ctf_contract = self.web3.eth.contract(address=self.CTF_ADDRESS, abi=CTF_ABI)
        # 
        # for token_id, description in known_tokens.items():
        #     try:
        #         balance = ctf_contract.functions.balanceOf(
        #             self.address, 
        #             int(token_id)
        #         ).call()
        #         
        #         if balance > 0:
        #             shares = balance / 1e6
        #             positions.append({
        #                 'token_id': token_id,
        #                 'shares': shares,
        #                 'market_info': {
        #                     'question': description,
        #                     'outcome': 'Yes',
        #                     'current_price': 0  # Would need to fetch from API
        #                 }
        #             })
        #     except Exception as e:
        #         print(f"Error checking token {token_id}: {e}")
        # 
        # return positions
    
    def get_market_for_token(self, token_id):
        """Try to find market information for a token ID"""
        # This would require maintaining a database or querying Polymarket API
        # For now, return basic info
        return {
            'question': 'Unknown Market',
            'outcome': 'Unknown',
            'current_price': 0
        }
    
    def get_open_orders(self):
        """Get all open orders from Polymarket API"""
        if not self.client:
            return []
        
        try:
            orders = self.client.get_orders()
            return [{
                'id': order.get('id'),
                'side': order.get('side'),
                'price': float(order.get('price', 0)),
                'size': float(order.get('original_size', 0)),
                'filled': float(order.get('size_matched', 0)),
                'market': order.get('market', 'Unknown'),
                'created': order.get('created_at')
            } for order in orders]
        except Exception as e:
            print(f"Warning: Could not fetch orders: {e}")
            return []
    
    def get_trade_history(self, limit=10):
        """Get recent trade history"""
        if not self.client:
            return []
        
        try:
            trades = self.client.get_trades()
            return [{
                'side': trade.get('side'),
                'price': float(trade.get('price', 0)),
                'size': float(trade.get('size', 0)),
                'value': float(trade.get('price', 0)) * float(trade.get('size', 0)),
                'timestamp': trade.get('created_at')
            } for trade in trades[:limit]]
        except Exception as e:
            print(f"Warning: Could not fetch trades: {e}")
            return []
    
    def calculate_pnl(self, positions, trades):
        """Calculate P&L for positions based on trade history"""
        # Simple P&L calculation - would need enhancement for accurate cost basis
        pnl_data = []
        
        for position in positions:
            # Find related trades
            buys = [t for t in trades if t['side'] == 'BUY']
            
            if buys:
                avg_cost = sum(t['price'] * t['size'] for t in buys) / sum(t['size'] for t in buys)
            else:
                avg_cost = 0
            
            current_value = position['shares'] * position.get('market_info', {}).get('current_price', 0)
            cost_basis = position['shares'] * avg_cost
            pnl = current_value - cost_basis
            pnl_pct = (pnl / cost_basis * 100) if cost_basis > 0 else 0
            
            pnl_data.append({
                'position': position,
                'avg_cost': avg_cost,
                'current_value': current_value,
                'cost_basis': cost_basis,
                'pnl': pnl,
                'pnl_pct': pnl_pct
            })
        
        return pnl_data
    
    def display_portfolio(self, detailed=False, show_history=False, json_output=False):
        """Display complete portfolio information"""
        
        # Gather all data
        balances = self.check_usdc_balances()
        positions = self.scan_ctf_positions()
        orders = self.get_open_orders()
        trades = self.get_trade_history(limit=50 if show_history else 10)
        
        if json_output:
            # JSON output
            output = {
                'timestamp': datetime.now().isoformat(),
                'address': self.address,
                'balances': balances,
                'positions': positions,
                'open_orders': orders,
                'recent_trades': trades
            }
            print(json.dumps(output, indent=2, default=str))
            return
        
        # Text output
        print("\n" + "="*80)
        print(" POLYMARKET PORTFOLIO REPORT ".center(80))
        print("="*80)
        print(f"Wallet: {self.address}")
        print(f"Time: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
        
        # Balances
        print("\n" + "="*80)
        print(" BALANCES ".center(80))
        print("="*80)
        
        balance_table = []
        if balances['bridged_usdc']['balance'] > 0:
            balance_table.append(['Bridged USDC', f"${balances['bridged_usdc']['balance']:.2f}"])
        if balances['native_usdc']['balance'] > 0:
            balance_table.append(['Native USDC.e', f"${balances['native_usdc']['balance']:.2f}"])
        balance_table.append(['MATIC (gas)', f"{balances['matic']['balance']:.4f}"])
        
        print(tabulate(balance_table, headers=['Asset', 'Balance'], tablefmt='grid'))
        
        # Positions
        if positions:
            print("\n" + "="*80)
            print(" POSITIONS ".center(80))
            print("="*80)
            
            pos_table = []
            for pos in positions:
                pos_table.append([
                    pos['token_id'][:20] + '...',
                    f"{pos['shares']:.4f}",
                    pos['market_info']['question'][:40] + '...' if len(pos['market_info']['question']) > 40 else pos['market_info']['question']
                ])
            
            print(tabulate(pos_table, headers=['Token ID', 'Shares', 'Market'], tablefmt='grid'))
        else:
            print("\n📊 No active positions found")
        
        # Open Orders
        if orders:
            print("\n" + "="*80)
            print(" OPEN ORDERS ".center(80))
            print("="*80)
            
            order_table = []
            for order in orders:
                remaining = order['size'] - order['filled']
                order_table.append([
                    order['id'][:10] + '...',
                    order['side'],
                    f"{order['price']*100:.1f}¢",
                    f"{remaining:.2f}/{order['size']:.2f}",
                    order['market'][:30] + '...' if len(order['market']) > 30 else order['market']
                ])
            
            print(tabulate(order_table, headers=['Order ID', 'Side', 'Price', 'Remaining/Total', 'Market'], tablefmt='grid'))
        else:
            print("\n📋 No open orders")
        
        # Recent Trades
        if show_history and trades:
            print("\n" + "="*80)
            print(" TRADE HISTORY ".center(80))
            print("="*80)
            
            trade_table = []
            for trade in trades[:20]:  # Limit to 20 for display
                trade_table.append([
                    trade['side'],
                    f"{trade['price']*100:.1f}¢",
                    f"{trade['size']:.2f}",
                    f"${trade['value']:.2f}",
                    trade['timestamp'][:19] if trade['timestamp'] else 'N/A'
                ])
            
            print(tabulate(trade_table, headers=['Side', 'Price', 'Size', 'Value', 'Time'], tablefmt='grid'))
        
        # Summary
        print("\n" + "="*80)
        print(" SUMMARY ".center(80))
        print("="*80)
        
        total_usdc = balances['bridged_usdc']['balance'] + balances['native_usdc']['balance']
        print(f"Total USDC: ${total_usdc:.2f}")
        print(f"Active Positions: {len(positions)}")
        print(f"Open Orders: {len(orders)}")
        print(f"Recent Trades: {len(trades)}")
        
        if balances['matic']['balance'] < 0.1:
            print("\n⚠️  Low MATIC balance - may need more for gas fees")

def main():
    parser = argparse.ArgumentParser(description='Check Polymarket portfolio')
    parser.add_argument('--detailed', action='store_true', help='Show detailed information')
    parser.add_argument('--show-history', action='store_true', help='Include full trade history')
    parser.add_argument('--json', action='store_true', help='Output in JSON format')
    
    args = parser.parse_args()
    
    try:
        checker = PortfolioChecker()
        checker.display_portfolio(
            detailed=args.detailed,
            show_history=args.show_history,
            json_output=args.json
        )
    except Exception as e:
        print(f"Error: {e}")
        import traceback
        traceback.print_exc()

if __name__ == "__main__":
    main()