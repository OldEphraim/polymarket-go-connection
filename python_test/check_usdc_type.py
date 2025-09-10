from web3 import Web3
from eth_account import Account
import os
from dotenv import load_dotenv

load_dotenv('../.env')

def check_usdc_type():
    """Check which type of USDC is in the wallet"""
    
    print("="*70)
    print("CHECKING YOUR USDC TYPE")
    print("="*70)
    
    # Connect to Polygon
    w3 = Web3(Web3.HTTPProvider("https://polygon-rpc.com"))
    
    # Get account
    private_key = os.getenv("PRIVATE_KEY")
    account = Account.from_key(private_key)
    address = account.address
    
    print(f"Your address: {address}")
    
    # USDC contracts
    USDC_BRIDGED = "0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174"  # Bridged USDC (what Polymarket API wants)
    USDC_NATIVE = "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359"   # USDC.e (native Polygon)
    
    # Polymarket contracts
    CTF_EXCHANGE = "0x4bFb41d5B3570DeFd03C39a9A4D8dE6Bd8B8982E"
    NEG_RISK_EXCHANGE = "0xC5d563A36AE78145C45a50134d48A1215220f80a"
    
    # ABI for balance and allowance
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
        },
        {
            "constant": True,
            "inputs": [],
            "name": "symbol",
            "outputs": [{"name": "", "type": "string"}],
            "type": "function"
        }
    ]
    
    print("\n" + "-"*70)
    print("USDC BALANCES:")
    print("-"*70)
    
    # Check bridged USDC
    bridged_contract = w3.eth.contract(address=USDC_BRIDGED, abi=ABI)
    bridged_balance = bridged_contract.functions.balanceOf(address).call()
    bridged_symbol = bridged_contract.functions.symbol().call()
    print(f"\n1. Bridged USDC (PoS)")
    print(f"   Contract: {USDC_BRIDGED}")
    print(f"   Symbol: {bridged_symbol}")
    print(f"   Balance: ${bridged_balance / 1e6:.2f}")
    
    if bridged_balance > 0:
        print("   ✅ THIS IS YOUR ACTIVE USDC")
        
        # Check allowances
        ctf_allowance = bridged_contract.functions.allowance(address, CTF_EXCHANGE).call()
        neg_allowance = bridged_contract.functions.allowance(address, NEG_RISK_EXCHANGE).call()
        
        print(f"\n   Allowances:")
        print(f"   - CTF Exchange: ${ctf_allowance / 1e6:.2f}")
        print(f"   - Neg Risk Exchange: ${neg_allowance / 1e6:.2f}")
        
        if ctf_allowance == 0 or neg_allowance == 0:
            print("\n   ⚠️  CONTRACTS NOT APPROVED!")
            print("   You need to run the approval script for bridged USDC")
    
    # Check native USDC.e
    native_contract = w3.eth.contract(address=USDC_NATIVE, abi=ABI)
    native_balance = native_contract.functions.balanceOf(address).call()
    native_symbol = native_contract.functions.symbol().call()
    print(f"\n2. Native USDC.e")
    print(f"   Contract: {USDC_NATIVE}")
    print(f"   Symbol: {native_symbol}")
    print(f"   Balance: ${native_balance / 1e6:.2f}")
    
    if native_balance > 0:
        print("   ✅ THIS IS YOUR ACTIVE USDC")
        
        # Check allowances
        ctf_allowance = native_contract.functions.allowance(address, CTF_EXCHANGE).call()
        neg_allowance = native_contract.functions.allowance(address, NEG_RISK_EXCHANGE).call()
        
        print(f"\n   Allowances:")
        print(f"   - CTF Exchange: ${ctf_allowance / 1e6:.2f}")
        print(f"   - Neg Risk Exchange: ${neg_allowance / 1e6:.2f}")
    
    # Diagnosis
    print("\n" + "="*70)
    print("WHAT TO DO NEXT:")
    print("="*70)
    
    if bridged_balance > 0:
        print("✅ You have bridged USDC - this is what Polymarket API expects!")
        
        if ctf_allowance == 0 or neg_allowance == 0:
            print("\n1. Run the approval script with:")
            print('   USDC_ADDRESS = "0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174"')
            print("\n2. Then try placing your bet")
        else:
            print("\nYour USDC is ready! Try placing a bet now.")
            print("If it still fails, the issue is with the py-clob-client initialization.")
    
    elif native_balance > 0:
        print("❌ You have USDC.e (native) - Polymarket API might not recognize this")
        print("\nYou need to swap to bridged USDC on QuickSwap")
    
    else:
        print("❌ No USDC found in your wallet!")

if __name__ == "__main__":
    check_usdc_type()

