import os
from py_clob_client.client import ClobClient
client = ClobClient("https://clob.polymarket.com", key=os.getenv("PRIVATE_KEY"), chain_id=137)
print(client)  # See if it even initializes