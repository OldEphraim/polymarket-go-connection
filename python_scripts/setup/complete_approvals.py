#!/usr/bin/env python3
"""
Complete Polymarket Contract Approvals
=======================================

This script approves the necessary smart contracts to trade on Polymarket.
It needs to be run ONCE per wallet address before you can start trading.

What this does:
1. Approves USDC spending for Polymarket exchange contracts
2. Approves CTF (Conditional Token Framework) token transfers
3. Sets up all necessary permissions for trading

IMPORTANT NOTES:
- This costs MATIC for gas fees (approximately 0.1-0.3 MATIC total)
- You need USDC in your wallet before trading (this just approves spending)
- Run this ONCE per wallet - approvals persist forever
- If you switch between USDC types, you may need to run again

Contract Addresses:
- USDC (Bridged): 0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174
- USDC.e (Native): 0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359
- CTF Tokens: 0x4D97DCd97eC945f40cF65F87097ACe5EA0476045

Exchange Contracts Being Approved:
- CTF Exchange: 0x4bFb41d5B3570DeFd03C39a9A4D8dE6Bd8B8982E
- Neg Risk Exchange: 0xC5d563A36AE78145C45a50134d48A1215220f80a
- Neg Risk Adapter: 0xd91E80cF2E7be2e162c6513ceD06f1dD0dA35296

Usage:
    python complete_approvals.py [--usdc-type bridged|native] [--check-only]
    
Options:
    --usdc-type    Which USDC to approve (default: bridged)
    --check-only   Just check current approvals without modifying
    --gas-price    Override gas price in gwei (default: auto)
"""

import os
import sys
import argparse
from web3 import Web3
from eth_account import Account
from dotenv import load_dotenv
from decimal import Decimal

load_dotenv()

class ApprovalManager:
    """Manages smart contract approvals for Polymarket trading"""
    
    def __init__(self, usdc_type='bridged'):
        """
        Initialize the approval manager
        
        Args:
            usdc_type: 'bridged' for standard USDC, 'native' for USDC.e
        """
        self.w3 = Web3(Web3.HTTPProvider("https://polygon-rpc.com"))
        
        if not self.w3.is_connected():
            raise ConnectionError("Cannot connect to Polygon RPC")
        
        # Get account from private key
        self.private_key = os.getenv("PRIVATE_KEY")
        if not self.private_key:
            raise ValueError("PRIVATE_KEY not found in .env file")
        
        self.account = Account.from_key(self.private_key)
        self.address = self.account.address
        
        # USDC contract addresses
        self.USDC_CONTRACTS = {
            'bridged': "0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174",  # Standard bridged USDC
            'native': "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359"     # USDC.e (native to Polygon)
        }
        
        # Select USDC type
        self.usdc_type = usdc_type
        self.usdc_address = self.USDC_CONTRACTS[usdc_type]
        
        # CTF token contract (same for all)
        self.CTF_ADDRESS = "0x4D97DCd97eC945f40cF65F87097ACe5EA0476045"
        
        # Polymarket exchange contracts that need approval
        self.CONTRACTS_TO_APPROVE = {
            "CTF Exchange": "0x4bFb41d5B3570DeFd03C39a9A4D8dE6Bd8B8982E",
            "Neg Risk Exchange": "0xC5d563A36AE78145C45a50134d48A1215220f80a",
            "Neg Risk Adapter": "0xd91E80cF2E7be2e162c6513ceD06f1dD0dA35296"
        }
        
        # ABIs for approval functions
        self.ERC20_ABI = [{
            "constant": False,
            "inputs": [
                {"name": "_spender", "type": "address"},
                {"name": "_value", "type": "uint256"}
            ],
            "name": "approve",
            "outputs": [{"name": "", "type": "bool"}],
            "type": "function"
        }, {
            "constant": True,
            "inputs": [
                {"name": "_owner", "type": "address"},
                {"name": "_spender", "type": "address"}
            ],
            "name": "allowance",
            "outputs": [{"name": "", "type": "uint256"}],
            "type": "function"
        }]
        
        self.ERC1155_ABI = [{
            "inputs": [
                {"name": "operator", "type": "address"},
                {"name": "approved", "type": "bool"}
            ],
            "name": "setApprovalForAll",
            "outputs": [],
            "type": "function"
        }, {
            "constant": True,
            "inputs": [
                {"name": "account", "type": "address"},
                {"name": "operator", "type": "address"}
            ],
            "name": "isApprovedForAll",
            "outputs": [{"name": "", "type": "bool"}],
            "type": "function"
        }]
    
    def check_balances(self):
        """Check MATIC and USDC balances"""
        matic_balance = self.w3.eth.get_balance(self.address) / 1e18
        
        usdc_contract = self.w3.eth.contract(address=self.usdc_address, abi=self.ERC20_ABI)
        usdc_balance = usdc_contract.functions.balanceOf(self.address).call() / 1e6
        
        return matic_balance, usdc_balance
    
    def check_approvals(self):
        """Check current approval status"""
        usdc_contract = self.w3.eth.contract(address=self.usdc_address, abi=self.ERC20_ABI)
        ctf_contract = self.w3.eth.contract(address=self.CTF_ADDRESS, abi=self.ERC1155_ABI)
        
        results = {}
        
        for name, spender in self.CONTRACTS_TO_APPROVE.items():
            # Check USDC allowance
            usdc_allowance = usdc_contract.functions.allowance(self.address, spender).call()
            
            # Check CTF approval
            ctf_approved = ctf_contract.functions.isApprovedForAll(self.address, spender).call()
            
            results[name] = {
                'address': spender,
                'usdc_allowance': usdc_allowance / 1e6 if usdc_allowance > 0 else 0,
                'ctf_approved': ctf_approved
            }
        
        return results
    
    def approve_contract(self, contract_name, contract_address, gas_price_gwei=None):
        """
        Approve both USDC and CTF for a specific contract
        
        Args:
            contract_name: Human-readable name
            contract_address: Address to approve
            gas_price_gwei: Optional gas price override
        """
        print(f"\n{'='*60}")
        print(f"Approving {contract_name}")
        print(f"Address: {contract_address}")
        print("-"*60)
        
        # Get gas price
        if gas_price_gwei:
            gas_price = self.w3.to_wei(gas_price_gwei, 'gwei')
        else:
            gas_price = self.w3.eth.gas_price
        
        # Approve USDC
        print("1. Approving USDC...")
        try:
            usdc_contract = self.w3.eth.contract(address=self.usdc_address, abi=self.ERC20_ABI)
            
            # Check current allowance
            current_allowance = usdc_contract.functions.allowance(self.address, contract_address).call()
            if current_allowance > 0:
                print(f"   ✓ Already approved (allowance: {current_allowance/1e6:.2f} USDC)")
            else:
                # Build transaction for max approval
                max_int = 2**256 - 1
                nonce = self.w3.eth.get_transaction_count(self.address)
                
                txn = usdc_contract.functions.approve(
                    contract_address,
                    max_int
                ).build_transaction({
                    'chainId': 137,
                    'from': self.address,
                    'nonce': nonce,
                    'gas': 100000,
                    'gasPrice': gas_price
                })
                
                # Sign and send
                signed_txn = self.w3.eth.account.sign_transaction(txn, private_key=self.private_key)
                tx_hash = self.w3.eth.send_raw_transaction(signed_txn.raw_transaction)
                
                print(f"   TX: {tx_hash.hex()}")
                print("   Waiting for confirmation...")
                
                receipt = self.w3.eth.wait_for_transaction_receipt(tx_hash, timeout=120)
                
                if receipt['status'] == 1:
                    print(f"   ✅ USDC approved for {contract_name}")
                else:
                    print(f"   ❌ USDC approval failed")
                    
        except Exception as e:
            print(f"   ❌ Error approving USDC: {e}")
        
        # Approve CTF tokens
        print("2. Approving CTF tokens...")
        try:
            ctf_contract = self.w3.eth.contract(address=self.CTF_ADDRESS, abi=self.ERC1155_ABI)
            
            # Check if already approved
            is_approved = ctf_contract.functions.isApprovedForAll(self.address, contract_address).call()
            if is_approved:
                print(f"   ✓ Already approved")
            else:
                nonce = self.w3.eth.get_transaction_count(self.address)
                
                txn = ctf_contract.functions.setApprovalForAll(
                    contract_address,
                    True
                ).build_transaction({
                    'chainId': 137,
                    'from': self.address,
                    'nonce': nonce,
                    'gas': 100000,
                    'gasPrice': gas_price
                })
                
                signed_txn = self.w3.eth.account.sign_transaction(txn, private_key=self.private_key)
                tx_hash = self.w3.eth.send_raw_transaction(signed_txn.raw_transaction)
                
                print(f"   TX: {tx_hash.hex()}")
                print("   Waiting for confirmation...")
                
                receipt = self.w3.eth.wait_for_transaction_receipt(tx_hash, timeout=120)
                
                if receipt['status'] == 1:
                    print(f"   ✅ CTF tokens approved for {contract_name}")
                else:
                    print(f"   ❌ CTF approval failed")
                    
        except Exception as e:
            print(f"   ❌ Error approving CTF: {e}")
    
    def run_approvals(self, gas_price_gwei=None):
        """Run all approvals"""
        print("="*70)
        print("POLYMARKET CONTRACT APPROVAL SETUP")
        print("="*70)
        print(f"Wallet: {self.address}")
        print(f"USDC Type: {self.usdc_type} ({self.usdc_address})")
        print(f"Network: Polygon Mainnet (Chain ID: 137)")
        
        # Check balances
        matic, usdc = self.check_balances()
        print(f"\nBalances:")
        print(f"  MATIC: {matic:.4f} (for gas)")
        print(f"  USDC ({self.usdc_type}): ${usdc:.2f}")
        
        if matic < 0.1:
            print("\n⚠️  WARNING: Low MATIC balance!")
            print("You need at least 0.1 MATIC for gas fees")
            confirm = input("Continue anyway? (y/n): ")
            if confirm.lower() != 'y':
                return
        
        # Show what will be approved
        print(f"\nThis will approve 3 Polymarket contracts to:")
        print(f"  1. Spend your {self.usdc_type} USDC")
        print(f"  2. Transfer your CTF tokens (shares)")
        print(f"\nEstimated gas cost: 0.1-0.3 MATIC")
        
        confirm = input("\n⚠️  Proceed with approvals? (y/n): ")
        if confirm.lower() != 'y':
            print("Cancelled")
            return
        
        # Run approvals
        for name, address in self.CONTRACTS_TO_APPROVE.items():
            self.approve_contract(name, address, gas_price_gwei)
        
        print("\n" + "="*70)
        print("✅ APPROVAL PROCESS COMPLETE!")
        print("="*70)
        print("\nYou can now trade on Polymarket using this wallet.")
        print("Run 'python place_trade.py --help' to start trading.")
    
    def display_approval_status(self):
        """Display current approval status"""
        print("="*70)
        print("CURRENT APPROVAL STATUS")
        print("="*70)
        print(f"Wallet: {self.address}")
        print(f"USDC Type: {self.usdc_type}")
        
        approvals = self.check_approvals()
        
        print("\n" + "-"*70)
        for name, status in approvals.items():
            print(f"\n{name}:")
            print(f"  Address: {status['address']}")
            print(f"  USDC Allowance: {'✅ Unlimited' if status['usdc_allowance'] > 1e10 else f'${status["usdc_allowance"]:.2f}'}")
            print(f"  CTF Approval: {'✅ Approved' if status['ctf_approved'] else '❌ Not approved'}")
        
        # Check if fully approved
        all_approved = all(
            status['usdc_allowance'] > 0 and status['ctf_approved']
            for status in approvals.values()
        )
        
        print("\n" + "="*70)
        if all_approved:
            print("✅ All contracts are approved! You can trade.")
        else:
            print("❌ Some contracts need approval. Run without --check-only to approve.")

def main():
    parser = argparse.ArgumentParser(
        description='Approve Polymarket contracts for trading',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
    # Approve for bridged USDC (most common)
    python complete_approvals.py
    
    # Approve for native USDC.e
    python complete_approvals.py --usdc-type native
    
    # Just check current approvals
    python complete_approvals.py --check-only
    
    # Approve with custom gas price
    python complete_approvals.py --gas-price 50
        """
    )
    
    parser.add_argument(
        '--usdc-type',
        choices=['bridged', 'native'],
        default='bridged',
        help='Which USDC type to approve (default: bridged)'
    )
    parser.add_argument(
        '--check-only',
        action='store_true',
        help='Only check current approvals without modifying'
    )
    parser.add_argument(
        '--gas-price',
        type=float,
        help='Gas price in gwei (default: automatic)'
    )
    
    args = parser.parse_args()
    
    try:
        manager = ApprovalManager(usdc_type=args.usdc_type)
        
        if args.check_only:
            manager.display_approval_status()
        else:
            manager.run_approvals(gas_price_gwei=args.gas_price)
            
    except Exception as e:
        print(f"\n❌ Error: {e}")
        import traceback
        traceback.print_exc()

if __name__ == "__main__":
    main()

