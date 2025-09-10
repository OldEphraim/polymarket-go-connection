from web3 import Web3
import os
from dotenv import load_dotenv

load_dotenv('../.env')

def check_ctf_balance():
    web3 = Web3(Web3.HTTPProvider("https://polygon-rpc.com"))
    
    # CTF contract and your address
    CTF_ADDRESS = "0x4D97DCd97eC945f40cF65F87097ACe5EA0476045"
    YOUR_ADDRESS = "0x1A106d01540FB3a3B631226Bab98DA6959838c7b"
    
    # The token ID for "Yes" outcome
    TOKEN_ID = int("69295430172447163159793296100162053628203745168718129321397048391227518839454")
    
    # ERC1155 balance check ABI
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
    
    # CTF tokens have 6 decimals
    shares = balance / 1e6
    print(f"Your CTF token balance: {shares:.2f} shares")
    
    return shares

check_ctf_balance()