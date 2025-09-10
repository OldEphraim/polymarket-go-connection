import os
from py_clob_client.client import ClobClient
from py_clob_client.clob_types import OrderArgs, OrderType
from py_clob_client.order_builder.constants import SELL
from dotenv import load_dotenv
from web3 import Web3

load_dotenv('../.env')

def check_actual_shares():
    """Check actual CTF balance on-chain"""
    web3 = Web3(Web3.HTTPProvider("https://polygon-rpc.com"))
    
    CTF_ADDRESS = "0x4D97DCd97eC945f40cF65F87097ACe5EA0476045"
    YOUR_ADDRESS = "0x1A106d01540FB3a3B631226Bab98DA6959838c7b"
    TOKEN_ID = int("69295430172447163159793296100162053628203745168718129321397048391227518839454")
    
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
    balance = ctf.functions.balanceOf(YOUR_ADDRESS, TOKEN_ID).call()
    shares = balance / 1e6  # CTF tokens have 6 decimals
    
    return shares

def place_sell_order():
    """Place a sell order for Ukraine shares with correct share amount"""
    
    TOKEN_ID = "69295430172447163159793296100162053628203745168718129321397048391227518839454"
    
    # Check actual balance first
    print("="*70)
    print("CHECKING YOUR ACTUAL SHARE BALANCE")
    print("="*70)
    
    actual_shares = check_actual_shares()
    print(f"✓ You own: {actual_shares:.2f} shares")
    
    if actual_shares == 0:
        print("❌ No shares found. The buy order may not have executed.")
        return
    
    # Use actual share count
    SHARES_TO_SELL = actual_shares
    
    print(f"\nBased on 5 shares, you likely paid ~$1 per share")
    print("(The market price may have moved when your order executed)")
    print()
    
    # Calculate profit targets based on actual shares
    breakeven = 5.0 / SHARES_TO_SELL  # Get $5 back
    small_profit = 6.0 / SHARES_TO_SELL  # Get $6 back
    double_money = 10.0 / SHARES_TO_SELL  # Get $10 back
    
    print("Price targets for your 5 shares:")
    print(f"  Breakeven: ${breakeven:.2f} (get your $5 back)")
    print(f"  Small profit: ${small_profit:.2f} (get $6 back, $1 profit)")
    print(f"  Double money: ${double_money:.2f} (get $10 back)")
    print()
    
    # Get user's desired sell price
    print("Enter your sell price:")
    print("  - As decimal: 0.15 for 15¢, 0.99 for 99¢, 1.20 for $1.20")
    print("  - Or in cents: 15 for 15¢, 99 for 99¢, 120 for $1.20")
    print("  - Press Enter to use $1.20 (to get $6 back)")
    
    user_input = input("Sell price: ").strip()
    
    if not user_input:
        SELL_PRICE = 1.20  # Default to $1.20 for small profit
    else:
        try:
            # Determine if input is in decimal or cents format
            input_val = float(user_input)
            
            if input_val < 1.0:
                # If less than 1, assume it's already in decimal format (0.15 = 15¢)
                SELL_PRICE = input_val
                print(f"  Interpreted as: ${SELL_PRICE:.2f}")
            else:
                # If 1 or greater, assume it's in cents (15 = 15¢, 120 = $1.20)
                SELL_PRICE = input_val / 100
                print(f"  Interpreted as: ${SELL_PRICE:.2f} ({input_val}¢)")
                
        except ValueError:
            print("Invalid input, using default $1.20")
            SELL_PRICE = 1.20
    
    # Polymarket prices must be between 0.01 and 0.99
    if SELL_PRICE > 0.99:
        print(f"⚠️  Price ${SELL_PRICE:.2f} exceeds maximum of $0.99")
        SELL_PRICE = 0.99
        print(f"   Adjusted to maximum: $0.99")
    
    # Calculate returns
    total_return = SHARES_TO_SELL * SELL_PRICE
    profit = total_return - 5.0
    
    print(f"\n📊 Sell Order Details:")
    print(f"  Selling: {SHARES_TO_SELL:.2f} shares")
    print(f"  Price: ${SELL_PRICE:.2f} per share")
    print(f"  Total return: ${total_return:.2f}")
    print(f"  Profit/Loss: ${profit:+.2f}")
    print(f"  Order type: GTC (Good Till Cancelled)")
    
    confirm = input("\n⚠️  Place this sell order? (y/n): ")
    if confirm.lower() != 'y':
        print("Order cancelled")
        return
    
    # Initialize client
    print("\nConnecting to Polymarket...")
    
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
        print("✓ Connected")
        
        # Optional: Check positions via API
        try:
            print("\nChecking API positions...")
            positions = client.get_positions()
            if positions:
                print(f"API shows positions: {positions}")
        except:
            print("Could not fetch positions from API, continuing...")
        
        # Create sell order with EXACT share amount
        print("\nCreating sell order...")
        order_args = OrderArgs(
            token_id=TOKEN_ID,
            price=SELL_PRICE,
            size=SHARES_TO_SELL,  # Using exact share count
            side=SELL
        )
        
        signed_order = client.create_order(order_args)
        print("✓ Order signed")
        
        print("Submitting order...")
        resp = client.post_order(signed_order, OrderType.GTC)
        
        print("\n" + "="*70)
        print("✅ SELL ORDER PLACED!")
        print("="*70)
        print(f"Order ID: {resp.get('orderID', 'N/A')}")
        print(f"Status: {resp.get('status', 'N/A')}")
        print()
        print("Your sell order will remain open until:")
        print("  - Someone buys at your price")
        print("  - You cancel it")
        print("  - The market resolves")
        print()
        print(f"If filled at ${SELL_PRICE:.2f}, you'll receive ${total_return:.2f}")
        print("\nCheck your orders at: https://polymarket.com/portfolio")
        
    except Exception as e:
        print(f"\n❌ Error: {e}")
        
        if "not enough balance" in str(e).lower():
            print("\nTroubleshooting:")
            print("1. Wait 5-10 minutes for the API to sync")
            print("2. Try placing a manual sell order on polymarket.com first")
            print("3. Check if you need to re-approve the CTF contract")
            
            # Try to provide more debug info
            print("\nDebug info:")
            print(f"  Token ID: {TOKEN_ID}")
            print(f"  Shares to sell: {SHARES_TO_SELL}")
            print(f"  Price: ${SELL_PRICE:.2f}")

def check_open_orders():
    """Check if you have any open orders"""
    print("\n" + "="*70)
    print("CHECKING OPEN ORDERS")
    print("="*70)
    
    host = "https://clob.polymarket.com"
    key = os.getenv("PRIVATE_KEY")
    
    try:
        client = ClobClient(host, key=key, chain_id=137)
        api_creds = client.create_or_derive_api_creds()
        client.set_api_creds(api_creds)
        
        orders = client.get_orders()
        if orders:
            print(f"You have {len(orders)} open order(s):")
            for order in orders:
                print(f"  - {order}")
        else:
            print("No open orders found")
    except Exception as e:
        print(f"Could not fetch orders: {e}")

if __name__ == "__main__":
    place_sell_order()
    
    # Optionally check open orders
    print("\nWould you like to check your open orders? (y/n): ", end="")
    if input().lower() == 'y':
        check_open_orders()

