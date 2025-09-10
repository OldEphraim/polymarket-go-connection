from py_clob_client.client import ClobClient
import os
from dotenv import load_dotenv

load_dotenv('../.env')

client = ClobClient("https://clob.polymarket.com", key=os.getenv("PRIVATE_KEY"), chain_id=137)
api_creds = client.create_or_derive_api_creds()
client.set_api_creds(api_creds)

# Get your trade history
trades = client.get_trades()
print("Your trades:")
for trade in trades:
    print(f"  Price: ${trade.get('price')}, Size: {trade.get('size')}, Side: {trade.get('side')}")
