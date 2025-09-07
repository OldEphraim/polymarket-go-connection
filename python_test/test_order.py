import os
import sys
sys.path.append('..')

from py_clob_client.client import ClobClient
from py_clob_client.clob_types import OrderArgs
from py_clob_client.order_builder.constants import BUY
from dotenv import load_dotenv

load_dotenv('../.env')

def main():
    private_key = os.getenv("PRIVATE_KEY")
    if not private_key:
        print("Error: PRIVATE_KEY not found in .env file")
        return
    
    print("Initializing Polymarket client...")
    
    try:
        client = ClobClient(
            host="https://clob.polymarket.com",
            key=private_key,
            chain_id=137
        )
        
        # Set up API credentials
        api_creds = client.derive_api_key()
        client.set_api_creds(api_creds)
        print(f"API Key: {api_creds.api_key}")
        
        # Use create_and_post_order which combines both steps
        token_id = "47867586153549284756888584589370825185710049194179739179851633771232196944664"
        
        order_args = OrderArgs(
            token_id=token_id,
            price=0.98,
            size=10.0,  # 10 cents
            side=BUY
        )
        
        print("\nUsing create_and_post_order method...")
        resp = client.create_and_post_order(order_args)
        print(f"Success! Response: {resp}")
        
    except Exception as e:
        print(f"\nError: {e}")
        
        # If still getting balance error, let's try to understand why
        if "balance" in str(e).lower():
            print("\nDebug info:")
            print("- You have $190 in Polymarket")
            print("- You've already approved USDC")
            print("- Trying to spend only $0.10")
            print("\nPossible issue: The Python client might be checking balance differently")
            print("or the token ID might be invalid/expired")

if __name__ == "__main__":
    main()
