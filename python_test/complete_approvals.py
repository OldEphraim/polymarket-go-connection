from web3 import Web3
from eth_account import Account
import os
from dotenv import load_dotenv

load_dotenv('../.env')

# Configuration
RPC_URL = "https://polygon-rpc.com"
PRIVATE_KEY = os.getenv("PRIVATE_KEY")
CHAIN_ID = 137

# Contract addresses
USDC_ADDRESS = "0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174"
CTF_ADDRESS = "0x4D97DCd97eC945f40cF65F87097ACe5EA0476045"

# Contracts to approve
CONTRACTS_TO_APPROVE = {
    "CTF Exchange": "0x4bFb41d5B3570DeFd03C39a9A4D8dE6Bd8B8982E",
    "Neg Risk Exchange": "0xC5d563A36AE78145C45a50134d48A1215220f80a",
    "Neg Risk Adapter": "0xd91E80cF2E7be2e162c6513ceD06f1dD0dA35296"
}

# ABIs
ERC20_APPROVE_ABI = [{"constant": False,"inputs": [{"name": "_spender","type": "address" },{ "name": "_value", "type": "uint256" }],"name": "approve","outputs": [{ "name": "", "type": "bool" }],"payable": False,"stateMutability": "nonpayable","type": "function"}]
ERC1155_SET_APPROVAL_ABI = [{"inputs": [{ "internalType": "address", "name": "operator", "type": "address" },{ "internalType": "bool", "name": "approved", "type": "bool" }],"name": "setApprovalForAll","outputs": [],"stateMutability": "nonpayable","type": "function"}]

def main():
    print("="*70)
    print("COMPLETE POLYMARKET APPROVALS FOR METAMASK")
    print("="*70)
    
    # Setup web3 - no middleware needed for Polygon
    web3 = Web3(Web3.HTTPProvider(RPC_URL))
    
    if not web3.is_connected():
        print("❌ Cannot connect to Polygon RPC")
        return
    
    # Get account
    account = Account.from_key(PRIVATE_KEY)
    address = account.address
    print(f"Your address: {address}")
    print(f"Connected to Polygon block: {web3.eth.block_number}")
    
    # Get contracts
    usdc = web3.eth.contract(address=USDC_ADDRESS, abi=ERC20_APPROVE_ABI)
    ctf = web3.eth.contract(address=CTF_ADDRESS, abi=ERC1155_SET_APPROVAL_ABI)
    
    # Check MATIC balance for gas
    matic_balance = web3.eth.get_balance(address)
    print(f"MATIC balance: {matic_balance / 1e18:.4f} MATIC")
    
    if matic_balance < 0.1 * 1e18:
        print("⚠️  Low MATIC balance. You need at least 0.1 MATIC for gas fees")
    
    print("\nThis will approve 6 contracts (3 for USDC, 3 for CTF tokens)")
    print("Estimated gas cost: ~0.3 MATIC total")
    
    confirm = input("\nProceed with approvals? (y/n): ")
    if confirm.lower() != 'y':
        print("Cancelled")
        return
    
    # Process approvals
    for contract_name, contract_address in CONTRACTS_TO_APPROVE.items():
        print(f"\n{'='*50}")
        print(f"Approving {contract_name}")
        print(f"Address: {contract_address}")
        print("-"*50)
        
        # Approve USDC
        try:
            print("1. Approving USDC...")
            nonce = web3.eth.get_transaction_count(address)
            
            # Max approval amount
            max_int = 2**256 - 1
            
            usdc_txn = usdc.functions.approve(
                contract_address, 
                max_int
            ).build_transaction({
                "chainId": CHAIN_ID, 
                "from": address, 
                "nonce": nonce,
                "gas": 100000,
                "gasPrice": web3.eth.gas_price
            })
            
            signed_usdc_tx = web3.eth.account.sign_transaction(usdc_txn, private_key=PRIVATE_KEY)
            usdc_tx_hash = web3.eth.send_raw_transaction(signed_usdc_tx.raw_transaction)
            
            print(f"   TX: {usdc_tx_hash.hex()}")
            print("   Waiting for confirmation...")
            
            usdc_receipt = web3.eth.wait_for_transaction_receipt(usdc_tx_hash, timeout=120)
            
            if usdc_receipt['status'] == 1:
                print(f"   ✅ USDC approved for {contract_name}")
            else:
                print(f"   ❌ USDC approval failed")
                
        except Exception as e:
            print(f"   ❌ Error approving USDC: {e}")
        
        # Approve CTF tokens
        try:
            print("2. Approving CTF tokens...")
            nonce = web3.eth.get_transaction_count(address)
            
            ctf_txn = ctf.functions.setApprovalForAll(
                contract_address, 
                True
            ).build_transaction({
                "chainId": CHAIN_ID, 
                "from": address, 
                "nonce": nonce,
                "gas": 100000,
                "gasPrice": web3.eth.gas_price
            })
            
            signed_ctf_tx = web3.eth.account.sign_transaction(ctf_txn, private_key=PRIVATE_KEY)
            ctf_tx_hash = web3.eth.send_raw_transaction(signed_ctf_tx.raw_transaction)
            
            print(f"   TX: {ctf_tx_hash.hex()}")
            print("   Waiting for confirmation...")
            
            ctf_receipt = web3.eth.wait_for_transaction_receipt(ctf_tx_hash, timeout=120)
            
            if ctf_receipt['status'] == 1:
                print(f"   ✅ CTF tokens approved for {contract_name}")
            else:
                print(f"   ❌ CTF approval failed")
                
        except Exception as e:
            print(f"   ❌ Error approving CTF: {e}")
    
    print("\n" + "="*70)
    print("✅ ALL APPROVALS COMPLETE!")
    print("="*70)
    print("\nYou can now trade on Polymarket using MetaMask!")
    print("Try placing your Ukraine bet again.")

if __name__ == "__main__":
    main()

