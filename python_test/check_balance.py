from web3 import Web3
from eth_account import Account
import os
from dotenv import load_dotenv
from py_clob_client.client import ClobClient

load_dotenv('../.env')

def check_on_chain_balances():
    """Check actual on-chain balances and allowances"""
    
    print("="*70)
    print("ON-CHAIN BALANCE CHECK")
    print("="*70)
    
    # Connect to Polygon
    w3 = Web3(Web3.HTTPProvider("https://polygon-rpc.com"))
    
    # Get account
    private_key = os.getenv("PRIVATE_KEY")
    account = Account.from_key(private_key)
    address = account.address
    
    print(f"Your address: {address}")
    
    # USDC contracts
    USDC_BRIDGED = "0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174"  # Bridged USDC
    USDC_NATIVE = "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359"   # USDC.e (native)
    
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
        }
    ]
    
    print("\n1. USDC BALANCES:")
    print("-"*50)
    
    # Check bridged USDC
    bridged_contract = w3.eth.contract(address=USDC_BRIDGED, abi=ABI)
    bridged_balance = bridged_contract.functions.balanceOf(address).call()
    print(f"Bridged USDC (0x2791...): ${bridged_balance / 1e6:.2f}")
    
    # Check native USDC.e
    native_contract = w3.eth.contract(address=USDC_NATIVE, abi=ABI)
    native_balance = native_contract.functions.balanceOf(address).call()
    print(f"Native USDC.e (0x3c49...): ${native_balance / 1e6:.2f}")
    
    print("\n2. ALLOWANCES FOR POLYMARKET:")
    print("-"*50)
    
    # Check allowances for bridged USDC
    print("Bridged USDC allowances:")
    ctf_allowance_bridged = bridged_contract.functions.allowance(address, CTF_EXCHANGE).call()
    neg_allowance_bridged = bridged_contract.functions.allowance(address, NEG_RISK_EXCHANGE).call()
    print(f"  CTF Exchange: ${ctf_allowance_bridged / 1e6:.2f}")
    print(f"  Neg Risk Exchange: ${neg_allowance_bridged / 1e6:.2f}")
    
    # Check allowances for native USDC.e
    print("\nNative USDC.e allowances:")
    ctf_allowance_native = native_contract.functions.allowance(address, CTF_EXCHANGE).call()
    neg_allowance_native = native_contract.functions.allowance(address, NEG_RISK_EXCHANGE).call()
    print(f"  CTF Exchange: ${ctf_allowance_native / 1e6:.2f}")
    print(f"  Neg Risk Exchange: ${neg_allowance_native / 1e6:.2f}")
    
    # Return the active USDC type
    if native_balance > 0:
        return "native", native_balance / 1e6, ctf_allowance_native / 1e6
    elif bridged_balance > 0:
        return "bridged", bridged_balance / 1e6, ctf_allowance_bridged / 1e6
    else:
        return "none", 0, 0

def check_clob_client():
    """Check what the CLOB client sees"""
    
    print("\n" + "="*70)
    print("CLOB CLIENT CHECK")
    print("="*70)
    
    private_key = os.getenv("PRIVATE_KEY")
    
    try:
        # Initialize with different configurations
        configs = [
            {"name": "Standard EOA", "signature_type": None, "funder": None},
            {"name": "With funder", "signature_type": 0, "funder": "0x1A106d01540FB3a3B631226Bab98DA6959838c7b"},
        ]
        
        for config in configs:
            print(f"\nTrying: {config['name']}")
            print("-"*30)
            
            try:
                if config['signature_type'] is not None:
                    client = ClobClient(
                        host="https://clob.polymarket.com",
                        key=private_key,
                        chain_id=137,
                        signature_type=config['signature_type'],
                        funder=config['funder']
                    )
                else:
                    client = ClobClient(
                        host="https://clob.polymarket.com",
                        key=private_key,
                        chain_id=137
                    )
                
                # Set API credentials
                api_creds = client.create_or_derive_api_creds()
                client.set_api_creds(api_creds)
                
                # Try to get balance
                balance_info = client.get_balance_allowance()
                print(f"✓ Success!")
                print(f"  Balance: ${balance_info.get('balance', 0)}")
                print(f"  Allowance: ${balance_info.get('allowance', 0)}")
                
            except Exception as e:
                print(f"✗ Failed: {e}")
                
    except Exception as e:
        print(f"Overall error: {e}")

def main():
    print("\n🔍 DIAGNOSING POLYMARKET BALANCE ISSUES")
    print("="*70)
    
    # Check on-chain balances
    usdc_type, balance, allowance = check_on_chain_balances()
    
    # Check CLOB client
    check_clob_client()
    
    # Diagnosis
    print("\n" + "="*70)
    print("DIAGNOSIS")
    print("="*70)
    
    if usdc_type == "native":
        print("✓ You have USDC.e (native)")
        print(f"✓ Balance: ${balance:.2f}")
        
        if allowance > 0:
            print(f"✓ Contracts approved: ${allowance:.2f}")
        else:
            print("✗ Contracts NOT approved for USDC.e")
            print("\nRun the approval script with USDC.e address:")
            print("  USDC_ADDRESS = '0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359'")
            
        print("\nThe py-clob-client might not properly support USDC.e")
        print("You may need to swap USDC.e to bridged USDC")
        
    elif usdc_type == "bridged":
        print("✓ You have bridged USDC")
        print(f"✓ Balance: ${balance:.2f}")
        
        if allowance > 0:
            print(f"✓ Contracts approved: ${allowance:.2f}")
        else:
            print("✗ Contracts NOT approved")
            
    else:
        print("✗ No USDC found in wallet!")
    
    print("\nPOSSIBLE SOLUTIONS:")
    print("1. If you have USDC.e but CLOB doesn't recognize it:")
    print("   - Swap USDC.e to bridged USDC on Uniswap/Quickswap")
    print("2. Make sure contracts are approved for the right USDC type")
    print("3. Try using the Polymarket web interface to verify your funds work")

if __name__ == "__main__":
    main()
