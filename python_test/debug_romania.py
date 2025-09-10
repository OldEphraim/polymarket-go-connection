#!/usr/bin/env python3
"""
Debug script to figure out what's causing the decimal precision error
"""

import os
import json
from py_clob_client.client import ClobClient
from py_clob_client.clob_types import OrderArgs, OrderType
from py_clob_client.order_builder.constants import BUY
from dotenv import load_dotenv

load_dotenv('../.env')

def test_order_creation():
    """Test creating an order with different decimal precisions"""
    
    # Romania token ID (from previous attempts)
    TOKEN_ID = "24774048549660093637249705878667532205573252986509496949873279924877879997872"
    
    print("="*70)
    print("DEBUGGING DECIMAL PRECISION ISSUE")
    print("="*70)
    
    # Test different combinations
    test_cases = [
        {"price": 0.99, "size": 5.0, "desc": "Simple integers as floats"},
        {"price": 0.99, "size": 5, "desc": "Size as integer"},
        {"price": 0.5, "size": 10.0, "desc": "Different price point"},
        {"price": 0.75, "size": 6.66, "desc": "Two decimals in size"},
        {"price": 0.80, "size": 6.25, "desc": "Clean division"},
    ]
    
    host = "https://clob.polymarket.com"
    key = os.getenv("PRIVATE_KEY")
    chain_id = 137
    
    if not key:
        print("❌ No private key found")
        return
    
    # Initialize client
    client = ClobClient(host, key=key, chain_id=chain_id)
    api_creds = client.create_or_derive_api_creds()
    client.set_api_creds(api_creds)
    print("✓ Connected to Polymarket\n")
    
    for i, test in enumerate(test_cases):
        print(f"Test {i+1}: {test['desc']}")
        print(f"  Price: {test['price']} (type: {type(test['price'])})")
        print(f"  Size: {test['size']} (type: {type(test['size'])})")
        
        try:
            # Create order args
            order_args = OrderArgs(
                token_id=TOKEN_ID,
                price=test['price'],
                size=test['size'],
                side=BUY
            )
            
            # Try to create the signed order
            signed_order = client.create_order(order_args)
            
            # Inspect the signed order
            print(f"  ✓ Order created successfully")
            
            # Let's see what's actually in the signed order
            if hasattr(signed_order, '__dict__'):
                print("  Order contents:")
                for key, value in signed_order.__dict__.items():
                    if 'amount' in key.lower() or 'price' in key.lower() or 'size' in key.lower():
                        print(f"    {key}: {value} (type: {type(value)})")
            
            # Try to submit as FOK (but cancel before confirming)
            confirm = input("\n  Try to submit this order? (y/n): ")
            if confirm.lower() == 'y':
                try:
                    resp = client.post_order(signed_order, OrderType.FOK)
                    print(f"  ✅ ORDER WORKED! Response: {resp}")
                    break
                except Exception as e:
                    print(f"  ❌ Submit failed: {e}")
            
        except Exception as e:
            print(f"  ❌ Create failed: {e}")
        
        print()

def inspect_order_object():
    """Inspect what the order object actually contains"""
    
    TOKEN_ID = "24774048549660093637249705878667532205573252986509496949873279924877879997872"
    
    print("\n" + "="*70)
    print("INSPECTING ORDER OBJECT STRUCTURE")
    print("="*70)
    
    host = "https://clob.polymarket.com"
    key = os.getenv("PRIVATE_KEY")
    chain_id = 137
    
    client = ClobClient(host, key=key, chain_id=chain_id)
    api_creds = client.create_or_derive_api_creds()
    client.set_api_creds(api_creds)
    
    # Create a simple order
    order_args = OrderArgs(
        token_id=TOKEN_ID,
        price=0.5,  # Simple 0.5
        size=10,     # Simple 10
        side=BUY
    )
    
    print("\nInput values:")
    print(f"  price: 0.5 (type: {type(0.5)})")
    print(f"  size: 10 (type: {type(10)})")
    
    # Create signed order
    signed_order = client.create_order(order_args)
    
    print("\nSigned order structure:")
    print(f"  Type: {type(signed_order)}")
    
    # Convert to dict/JSON to see structure
    if hasattr(signed_order, 'dict'):
        order_dict = signed_order.dict()
        print("\nOrder as dict:")
        print(json.dumps(order_dict, indent=2, default=str))
    
    # Check if it has a to_json method
    if hasattr(signed_order, 'json'):
        print("\nOrder as JSON:")
        print(signed_order.json())
    
    # List all attributes
    print("\nAll attributes:")
    for attr in dir(signed_order):
        if not attr.startswith('_'):
            value = getattr(signed_order, attr)
            if not callable(value):
                print(f"  {attr}: {value}")

if __name__ == "__main__":
    print("This script will help debug the decimal precision issue\n")
    print("1. Test different price/size combinations")
    print("2. Inspect order object structure")
    
    choice = input("\nChoice (1/2): ")
    
    if choice == "1":
        test_order_creation()
    elif choice == "2":
        inspect_order_object()
    else:
        print("Invalid choice")

