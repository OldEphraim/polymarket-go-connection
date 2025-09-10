#!/usr/bin/env python3
"""
Manage Polymarket orders - view, cancel, and replace
"""

import os
from py_clob_client.client import ClobClient
from py_clob_client.clob_types import OrderArgs, OrderType
from py_clob_client.order_builder.constants import BUY
from dotenv import load_dotenv

load_dotenv('../.env')

def get_client():
    """Get authenticated Polymarket client"""
    host = "https://clob.polymarket.com"
    key = os.getenv("PRIVATE_KEY")
    chain_id = 137
    
    client = ClobClient(host, key=key, chain_id=chain_id)
    api_creds = client.create_or_derive_api_creds()
    client.set_api_creds(api_creds)
    return client

def view_orders():
    """View all open orders with proper formatting"""
    print("\n" + "="*70)
    print("YOUR OPEN ORDERS")
    print("="*70)
    
    client = get_client()
    
    try:
        orders = client.get_orders()
        if not orders:
            print("No open orders")
            return []
        
        print(f"Found {len(orders)} open order(s):\n")
        
        for i, order in enumerate(orders):
            # Extract fields safely
            order_id = order.get('id', 'Unknown')
            side = order.get('side', 'Unknown')
            price = float(order.get('price', 0))
            size = float(order.get('original_size', 0))
            size_matched = float(order.get('size_matched', 0))
            remaining = size - size_matched
            market = order.get('market', 'Unknown')
            outcome = order.get('outcome', 'Unknown')
            created = order.get('created_at', 'Unknown')
            
            print(f"Order #{i+1}:")
            print(f"  ID: {order_id}")
            print(f"  Type: {side}")
            print(f"  Market: {market}")
            print(f"  Outcome: {outcome}")
            print(f"  Price: ${price:.3f} ({price*100:.1f}¢)")
            print(f"  Original size: {size:.2f} shares")
            print(f"  Filled: {size_matched:.2f} shares")
            print(f"  Remaining: {remaining:.2f} shares")
            print(f"  Created: {created}")
            print()
        
        return orders
    
    except Exception as e:
        print(f"Error fetching orders: {e}")
        return []

def cancel_order(order_id):
    """Cancel a specific order"""
    client = get_client()
    
    try:
        print(f"\nCancelling order {order_id}...")
        client.cancel(order_id)
        print("✅ Order cancelled successfully")
        return True
    except Exception as e:
        print(f"❌ Error cancelling order: {e}")
        return False

def cancel_all_orders():
    """Cancel all open orders"""
    client = get_client()
    
    try:
        print("\nCancelling all orders...")
        client.cancel_all()
        print("✅ All orders cancelled successfully")
        return True
    except Exception as e:
        print(f"❌ Error cancelling orders: {e}")
        return False

def place_new_senegal_order(price_cents):
    """Place a new Senegal buy order at specified price"""
    
    # Senegal "Yes" token
    TOKEN_ID = "88309419917004099428094188172685752533903668411511547229384883756825978807950"
    DOLLAR_AMOUNT = 5.0
    
    price = price_cents / 100.0
    shares = DOLLAR_AMOUNT / price
    
    print(f"\n" + "="*70)
    print("PLACING NEW SENEGAL ORDER")
    print("="*70)
    print(f"Price: ${price:.3f} ({price*100:.1f}¢)")
    print(f"Shares: {shares:.2f}")
    print(f"Total: ${DOLLAR_AMOUNT:.2f}")
    
    confirm = input("\nConfirm order? (y/n): ")
    if confirm.lower() != 'y':
        print("Cancelled")
        return
    
    client = get_client()
    
    try:
        order_args = OrderArgs(
            token_id=TOKEN_ID,
            price=price,
            size=shares,
            side=BUY
        )
        
        signed_order = client.create_order(order_args)
        resp = client.post_order(signed_order, OrderType.GTC)
        
        print("\n✅ Order placed successfully!")
        print(f"Order ID: {resp.get('orderID', 'Unknown')}")
        
    except Exception as e:
        print(f"❌ Error placing order: {e}")

def main():
    """Main menu"""
    while True:
        print("\n" + "="*70)
        print("POLYMARKET ORDER MANAGER")
        print("="*70)
        print("\nCurrent situation:")
        print("- Ukraine: Own 5 shares @ 61¢ (worth $0 - they drew)")
        print("- Senegal: Buy order @ 97.1¢ (market at 99.6¢)")
        print("\nOptions:")
        print("1. View all open orders")
        print("2. Cancel Senegal buy order and replace at 99.6¢")
        print("3. Cancel Ukraine sell order (pointless - worth $0)")
        print("4. Cancel ALL orders")
        print("5. Place custom Senegal order")
        print("6. Exit")
        
        choice = input("\nChoice (1-6): ").strip()
        
        if choice == "1":
            view_orders()
            
        elif choice == "2":
            orders = view_orders()
            
            # Find Senegal buy order
            senegal_order = None
            for order in orders:
                if order.get('side') == 'BUY' and 'sen' in order.get('market', '').lower():
                    senegal_order = order
                    break
            
            if senegal_order:
                order_id = senegal_order.get('id')
                print(f"\nFound Senegal buy order: {order_id}")
                
                if cancel_order(order_id):
                    print("\nPlacing new order at market price (99.6¢)...")
                    place_new_senegal_order(100)  # Actually 100¢ to ensure fill
            else:
                print("\nNo Senegal buy order found")
        
        elif choice == "3":
            orders = view_orders()
            
            # Find Ukraine sell order
            ukraine_order = None
            for order in orders:
                if order.get('side') == 'SELL':
                    ukraine_order = order
                    break
            
            if ukraine_order:
                order_id = ukraine_order.get('id')
                print(f"\nFound Ukraine sell order: {order_id}")
                print("Note: Ukraine drew, so these shares are worthless")
                confirm = input("Cancel anyway? (y/n): ")
                if confirm.lower() == 'y':
                    cancel_order(order_id)
            else:
                print("\nNo Ukraine sell order found")
        
        elif choice == "4":
            confirm = input("\nCancel ALL orders? (y/n): ")
            if confirm.lower() == 'y':
                cancel_all_orders()
        
        elif choice == "5":
            price_input = input("\nEnter price in cents (e.g., 99 for 99¢): ")
            try:
                price_cents = float(price_input)
                place_new_senegal_order(price_cents)
            except:
                print("Invalid price")
        
        elif choice == "6":
            print("\nExiting...")
            break
        
        else:
            print("\nInvalid choice")

if __name__ == "__main__":
    main()

