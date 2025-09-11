#!/usr/bin/env python3
"""
Order Management System for Polymarket
======================================

Manage open orders: view, cancel, modify, and monitor.

Usage:
    python order_manager.py [command] [options]
    
Commands:
    list                List all open orders
    cancel <order_id>   Cancel specific order
    cancel-all          Cancel all open orders
    modify <order_id>   Modify an existing order
    monitor             Live monitoring of orders
    
Examples:
    python order_manager.py list
    python order_manager.py cancel 0x123abc...
    python order_manager.py cancel-all --confirm
    python order_manager.py monitor --refresh 10
"""

import os
import sys
import time
import argparse
from datetime import datetime
from py_clob_client.client import ClobClient
from dotenv import load_dotenv
from tabulate import tabulate

load_dotenv()

class OrderManager:
    """Manage Polymarket orders"""
    
    def __init__(self):
        self.host = "https://clob.polymarket.com"
        self.key = os.getenv("PRIVATE_KEY")
        self.chain_id = 137
        
        if not self.key:
            raise ValueError("PRIVATE_KEY not found in .env file")
        
        # Initialize client
        self.client = ClobClient(self.host, key=self.key, chain_id=self.chain_id)
        api_creds = self.client.create_or_derive_api_creds()
        self.client.set_api_creds(api_creds)
    
    def get_orders(self):
        """Fetch all open orders"""
        try:
            orders = self.client.get_orders()
            return orders if orders else []
        except Exception as e:
            print(f"Error fetching orders: {e}")
            return []
    
    def list_orders(self, verbose=False):
        """Display all open orders"""
        orders = self.get_orders()
        
        if not orders:
            print("\n📋 No open orders")
            return
        
        print(f"\n📋 Found {len(orders)} open order(s):")
        print("="*100)
        
        if verbose:
            # Detailed view
            for i, order in enumerate(orders, 1):
                print(f"\nOrder #{i}")
                print("-"*50)
                print(f"ID: {order.get('id', 'N/A')}")
                print(f"Market: {order.get('market', 'N/A')}")
                print(f"Outcome: {order.get('outcome', 'N/A')}")
                print(f"Side: {order.get('side', 'N/A')}")
                print(f"Price: ${float(order.get('price', 0)):.3f} ({float(order.get('price', 0))*100:.1f}¢)")
                print(f"Size: {order.get('original_size', 'N/A')} shares")
                print(f"Filled: {order.get('size_matched', 0)} shares")
                remaining = float(order.get('original_size', 0)) - float(order.get('size_matched', 0))
                print(f"Remaining: {remaining:.2f} shares")
                print(f"Status: {order.get('status', 'N/A')}")
                print(f"Created: {order.get('created_at', 'N/A')}")
        else:
            # Table view
            table_data = []
            for order in orders:
                remaining = float(order.get('original_size', 0)) - float(order.get('size_matched', 0))
                table_data.append([
                    order.get('id', '')[:10] + '...',
                    order.get('side', ''),
                    f"{float(order.get('price', 0))*100:.1f}¢",
                    f"{remaining:.2f}",
                    order.get('market', '')[:30] + '...' if len(order.get('market', '')) > 30 else order.get('market', ''),
                    order.get('status', '')
                ])
            
            headers = ['Order ID', 'Side', 'Price', 'Remaining', 'Market', 'Status']
            print(tabulate(table_data, headers=headers, tablefmt='grid'))
        
        return orders
    
    def cancel_order(self, order_id):
        """Cancel a specific order"""
        try:
            print(f"\n🚫 Cancelling order {order_id}...")
            self.client.cancel(order_id)
            print("✅ Order cancelled successfully")
            return True
        except Exception as e:
            print(f"❌ Error cancelling order: {e}")
            return False
    
    def cancel_all_orders(self, confirm=False):
        """Cancel all open orders"""
        orders = self.get_orders()
        
        if not orders:
            print("\n📋 No orders to cancel")
            return
        
        print(f"\n⚠️  About to cancel {len(orders)} order(s):")
        self.list_orders()
        
        if not confirm:
            user_confirm = input("\nAre you sure you want to cancel ALL orders? (yes/no): ")
            if user_confirm.lower() != 'yes':
                print("Cancelled")
                return
        
        try:
            print("\n🚫 Cancelling all orders...")
            self.client.cancel_all()
            print("✅ All orders cancelled successfully")
        except Exception as e:
            print(f"❌ Error cancelling orders: {e}")
    
    def find_order(self, partial_id):
        """Find an order by partial ID"""
        orders = self.get_orders()
        
        matches = []
        for order in orders:
            if partial_id.lower() in order.get('id', '').lower():
                matches.append(order)
        
        if len(matches) == 0:
            return None
        elif len(matches) == 1:
            return matches[0]
        else:
            print(f"\n⚠️  Multiple orders match '{partial_id}':")
            for i, order in enumerate(matches, 1):
                print(f"{i}. {order.get('id')} - {order.get('side')} {order.get('original_size')} @ {order.get('price')}")
            
            choice = input("Select order number (or 'c' to cancel): ")
            if choice.lower() == 'c':
                return None
            
            try:
                idx = int(choice) - 1
                if 0 <= idx < len(matches):
                    return matches[idx]
            except:
                pass
            
            return None
    
    def monitor_orders(self, refresh_interval=10):
        """Live monitoring of orders"""
        print(f"\n📊 Monitoring orders (refresh every {refresh_interval}s)")
        print("Press Ctrl+C to stop\n")
        
        try:
            while True:
                # Clear screen (works on Unix/Mac)
                os.system('clear' if os.name == 'posix' else 'cls')
                
                print(f"POLYMARKET ORDER MONITOR - {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
                print("="*100)
                
                orders = self.get_orders()
                
                if not orders:
                    print("\n📋 No open orders")
                else:
                    # Summary
                    total_buy = sum(1 for o in orders if o.get('side') == 'BUY')
                    total_sell = sum(1 for o in orders if o.get('side') == 'SELL')
                    
                    print(f"\nSummary: {len(orders)} orders ({total_buy} buy, {total_sell} sell)")
                    print("-"*100)
                    
                    # Order table
                    table_data = []
                    for order in orders:
                        remaining = float(order.get('original_size', 0)) - float(order.get('size_matched', 0))
                        filled_pct = (float(order.get('size_matched', 0)) / float(order.get('original_size', 1))) * 100
                        
                        table_data.append([
                            order.get('id', '')[:8] + '...',
                            order.get('side', ''),
                            f"{float(order.get('price', 0))*100:.1f}¢",
                            f"{remaining:.1f}/{order.get('original_size', 0)}",
                            f"{filled_pct:.0f}%",
                            order.get('market', '')[:25] + '...' if len(order.get('market', '')) > 25 else order.get('market', ''),
                            order.get('status', '')
                        ])
                    
                    headers = ['Order ID', 'Side', 'Price', 'Remain/Total', 'Filled', 'Market', 'Status']
                    print(tabulate(table_data, headers=headers, tablefmt='grid'))
                
                print(f"\nRefreshing in {refresh_interval} seconds... (Ctrl+C to stop)")
                time.sleep(refresh_interval)
                
        except KeyboardInterrupt:
            print("\n\n✅ Monitoring stopped")
    
    def modify_order(self, order_id, new_price=None, new_size=None):
        """
        Modify an existing order (cancel and replace)
        
        Note: Polymarket doesn't support direct modification,
        so this cancels the old order and places a new one.
        """
        # Find the order
        order = self.find_order(order_id)
        if not order:
            print(f"❌ Order not found: {order_id}")
            return
        
        print(f"\n📝 Modifying order {order.get('id')}")
        print(f"Current: {order.get('side')} {order.get('original_size')} @ ${order.get('price')}")
        
        # Get new values
        if new_price is None:
            new_price_input = input(f"New price (current: {float(order.get('price'))*100:.1f}¢, press Enter to keep): ")
            if new_price_input:
                new_price = float(new_price_input) / 100
            else:
                new_price = float(order.get('price'))
        
        if new_size is None:
            remaining = float(order.get('original_size', 0)) - float(order.get('size_matched', 0))
            new_size_input = input(f"New size (current remaining: {remaining:.2f}, press Enter to keep): ")
            if new_size_input:
                new_size = float(new_size_input)
            else:
                new_size = remaining
        
        print(f"New: {order.get('side')} {new_size} @ ${new_price:.3f}")
        
        confirm = input("\nProceed with modification? (y/n): ")
        if confirm.lower() != 'y':
            print("Cancelled")
            return
        
        # Cancel old order
        print("\n1. Cancelling old order...")
        if not self.cancel_order(order.get('id')):
            print("❌ Failed to cancel old order")
            return
        
        # Place new order
        print("2. Placing new order...")
        # Note: This would need the place_trade functionality
        print("⚠️  New order placement not implemented in this script")
        print("   Use place_trade.py to place the new order")

def main():
    parser = argparse.ArgumentParser(
        description='Manage Polymarket orders',
        formatter_class=argparse.RawDescriptionHelpFormatter
    )
    
    subparsers = parser.add_subparsers(dest='command', help='Commands')
    
    # List command
    list_parser = subparsers.add_parser('list', help='List all open orders')
    list_parser.add_argument('--verbose', '-v', action='store_true', help='Show detailed information')
    
    # Cancel command
    cancel_parser = subparsers.add_parser('cancel', help='Cancel specific order')
    cancel_parser.add_argument('order_id', help='Order ID (or partial ID)')
    
    # Cancel all command
    cancel_all_parser = subparsers.add_parser('cancel-all', help='Cancel all open orders')
    cancel_all_parser.add_argument('--confirm', action='store_true', help='Skip confirmation prompt')
    
    # Monitor command
    monitor_parser = subparsers.add_parser('monitor', help='Live monitoring of orders')
    monitor_parser.add_argument('--refresh', type=int, default=10, help='Refresh interval in seconds')
    
    # Modify command
    modify_parser = subparsers.add_parser('modify', help='Modify an existing order')
    modify_parser.add_argument('order_id', help='Order ID (or partial ID)')
    modify_parser.add_argument('--price', type=float, help='New price in decimal (e.g., 0.75)')
    modify_parser.add_argument('--size', type=float, help='New size in shares')
    
    args = parser.parse_args()
    
    if not args.command:
        parser.print_help()
        sys.exit(1)
    
    try:
        manager = OrderManager()
        
        if args.command == 'list':
            manager.list_orders(verbose=args.verbose)
        
        elif args.command == 'cancel':
            # Try to find order by partial ID
            order = manager.find_order(args.order_id)
            if order:
                manager.cancel_order(order.get('id'))
            else:
                print(f"❌ Order not found: {args.order_id}")
        
        elif args.command == 'cancel-all':
            manager.cancel_all_orders(confirm=args.confirm)
        
        elif args.command == 'monitor':
            manager.monitor_orders(refresh_interval=args.refresh)
        
        elif args.command == 'modify':
            manager.modify_order(args.order_id, new_price=args.price, new_size=args.size)
        
    except Exception as e:
        print(f"\n❌ Error: {e}")
        import traceback
        traceback.print_exc()

if __name__ == "__main__":
    main()

