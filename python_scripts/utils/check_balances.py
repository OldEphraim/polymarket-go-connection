#!/usr/bin/env python3
"""
Check all balances: USDC, CTF tokens, and MATIC

Usage:
    python check_balances.py [--json] [--watch]
    
Options:
    --json    Output in JSON format
    --watch   Continuous monitoring mode
"""

import os
import sys
import json
import time
import argparse
from datetime import datetime
from web3 import Web3
from dotenv import load_dotenv
from tabulate import tabulate

load_dotenv()

class BalanceChecker:
    """Check all Polymarket-related balances"""
    
    def __init__(self):
        self.w3 = Web3(Web3.HTTPProvider("https://polygon-rpc.com"))
        self.address = os.getenv("WALLET_ADDRESS")
        
        if not self.w3.is_connected():
            raise ConnectionError("Cannot connect to Polygon RPC")
        
        # Contract addresses
        self.CONTRACTS = {
            'USDC_BRIDGED': "0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174",
            'USDC_NATIVE': "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359",
            'CTF': "0x4D97DCd97eC945f40cF65F87097ACe5EA0476045",
            'CTF_EXCHANGE': "0x4bFb41d5B3570DeFd03C39a9A4D8dE6Bd8B8982E",
            'NEG_RISK_EXCHANGE': "0xC5d563A36AE78145C45a50134d48A1215220f80a",
            'NEG_RISK_ADAPTER': "0xd91E80cF2E7be2e162c6513ceD06f1dD0dA35296"
        }
        
        # ABIs
        self.ERC20_ABI = [
            {
                "constant": True,
                "inputs": [{"name": "_owner", "type": "address"}],
                "name": "balanceOf",
                "outputs": [{"name": "", "type": "uint256"}],
                "type": "function"
            },
            {
                "constant": True,
                "inputs": [],
                "name": "symbol",
                "outputs": [{"name": "", "type": "string"}],
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
        
        self.ERC1155_ABI = [
            {
                "inputs": [
                    {"name": "account", "type": "address"},
                    {"name": "id", "type": "uint256"}
                ],
                "name": "balanceOf",
                "outputs": [{"name": "", "type": "uint256"}],
                "stateMutability": "view",
                "type": "function"
            },
            {
                "inputs": [
                    {"name": "account", "type": "address"},
                    {"name": "operator", "type": "address"}
                ],
                "name": "isApprovedForAll",
                "outputs": [{"name": "", "type": "bool"}],
                "stateMutability": "view",
                "type": "function"
            }
        ]
    
    def get_native_balances(self):
        """Get MATIC balance"""
        matic_wei = self.w3.eth.get_balance(self.address)
        matic = matic_wei / 1e18
        
        # Get current MATIC price (you could fetch from an API)
        matic_price = 0.50  # Placeholder
        
        return {
            'matic': {
                'balance': matic,
                'value_usd': matic * matic_price,
                'sufficient_for_gas': matic >= 0.1
            }
        }
    
    def get_usdc_balances(self):
        """Get all USDC balances and allowances"""
        results = {}
        
        # Check bridged USDC
        bridged_contract = self.w3.eth.contract(
            address=self.CONTRACTS['USDC_BRIDGED'], 
            abi=self.ERC20_ABI
        )
        bridged_balance = bridged_contract.functions.balanceOf(self.address).call() / 1e6
        
        results['bridged'] = {
            'balance': bridged_balance,
            'symbol': 'USDC',
            'address': self.CONTRACTS['USDC_BRIDGED'],
            'allowances': {}
        }
        
        # Check allowances for bridged USDC
        for name in ['CTF_EXCHANGE', 'NEG_RISK_EXCHANGE', 'NEG_RISK_ADAPTER']:
            allowance = bridged_contract.functions.allowance(
                self.address, 
                self.CONTRACTS[name]
            ).call() / 1e6
            results['bridged']['allowances'][name] = allowance > 0
        
        # Check native USDC.e
        native_contract = self.w3.eth.contract(
            address=self.CONTRACTS['USDC_NATIVE'], 
            abi=self.ERC20_ABI
        )
        native_balance = native_contract.functions.balanceOf(self.address).call() / 1e6
        
        results['native'] = {
            'balance': native_balance,
            'symbol': 'USDC.e',
            'address': self.CONTRACTS['USDC_NATIVE'],
            'allowances': {}
        }
        
        # Check allowances for native USDC
        for name in ['CTF_EXCHANGE', 'NEG_RISK_EXCHANGE', 'NEG_RISK_ADAPTER']:
            allowance = native_contract.functions.allowance(
                self.address, 
                self.CONTRACTS[name]
            ).call() / 1e6
            results['native']['allowances'][name] = allowance > 0
        
        # Determine which is active
        if bridged_balance > 0:
            results['active_type'] = 'bridged'
        elif native_balance > 0:
            results['active_type'] = 'native'
        else:
            results['active_type'] = None
        
        results['total'] = bridged_balance + native_balance
        
        return results
    
    def get_ctf_approvals(self):
        """Check CTF token approvals"""
        ctf_contract = self.w3.eth.contract(
            address=self.CONTRACTS['CTF'], 
            abi=self.ERC1155_ABI
        )
        
        approvals = {}
        for name in ['CTF_EXCHANGE', 'NEG_RISK_EXCHANGE', 'NEG_RISK_ADAPTER']:
            is_approved = ctf_contract.functions.isApprovedForAll(
                self.address,
                self.CONTRACTS[name]
            ).call()
            approvals[name] = is_approved
        
        return approvals
    
    def display_balances(self, json_output=False):
        """Display all balance information"""
        
        # Gather data
        native = self.get_native_balances()
        usdc = self.get_usdc_balances()
        ctf_approvals = self.get_ctf_approvals()
        
        if json_output:
            output = {
                'timestamp': datetime.now().isoformat(),
                'address': self.address,
                'native': native,
                'usdc': usdc,
                'ctf_approvals': ctf_approvals
            }
            print(json.dumps(output, indent=2))
            return
        
        # Text display
        print("\n" + "="*70)
        print(" BALANCE SUMMARY ".center(70))
        print("="*70)
        print(f"Wallet: {self.address}")
        print(f"Time: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
        
        # Native token
        print("\n" + "-"*70)
        print("NATIVE TOKEN (GAS):")
        print("-"*70)
        matic_info = native['matic']
        status = "✅" if matic_info['sufficient_for_gas'] else "⚠️ "
        print(f"{status} MATIC: {matic_info['balance']:.4f} (~${matic_info['value_usd']:.2f})")
        if not matic_info['sufficient_for_gas']:
            print("   WARNING: Need at least 0.1 MATIC for gas fees")
        
        # USDC balances
        print("\n" + "-"*70)
        print("USDC BALANCES:")
        print("-"*70)
        
        if usdc['bridged']['balance'] > 0:
            print(f"✅ Bridged USDC: ${usdc['bridged']['balance']:.2f}")
            # Check approvals
            all_approved = all(usdc['bridged']['allowances'].values())
            if not all_approved:
                print("   ⚠️  Some contracts not approved")
        
        if usdc['native']['balance'] > 0:
            print(f"✅ Native USDC.e: ${usdc['native']['balance']:.2f}")
            # Check approvals
            all_approved = all(usdc['native']['allowances'].values())
            if not all_approved:
                print("   ⚠️  Some contracts not approved")
        
        if usdc['total'] == 0:
            print("❌ No USDC found")
        else:
            print(f"\n💰 Total USDC: ${usdc['total']:.2f}")
        
        # Approval status
        print("\n" + "-"*70)
        print("APPROVAL STATUS:")
        print("-"*70)
        
        # Determine which USDC to check
        if usdc['active_type']:
            usdc_approvals = usdc[usdc['active_type']]['allowances']
            usdc_type = "Bridged USDC" if usdc['active_type'] == 'bridged' else "Native USDC.e"
            
            print(f"\n{usdc_type} Approvals:")
            for contract, approved in usdc_approvals.items():
                status = "✅" if approved else "❌"
                print(f"  {status} {contract}")
        
        print(f"\nCTF Token Approvals:")
        for contract, approved in ctf_approvals.items():
            status = "✅" if approved else "❌"
            print(f"  {status} {contract}")
        
        # Trading readiness
        print("\n" + "="*70)
        ready_to_trade = False
        
        if usdc['active_type']:
            usdc_ready = all(usdc[usdc['active_type']]['allowances'].values())
            ctf_ready = all(ctf_approvals.values())
            ready_to_trade = usdc_ready and ctf_ready and native['matic']['sufficient_for_gas']
        
        if ready_to_trade:
            print("✅ READY TO TRADE")
        else:
            print("❌ NOT READY TO TRADE")
            print("\nIssues to fix:")
            if not native['matic']['sufficient_for_gas']:
                print("  • Need more MATIC for gas")
            if usdc['total'] == 0:
                print("  • Need USDC tokens")
            elif usdc['active_type']:
                if not all(usdc[usdc['active_type']]['allowances'].values()):
                    print("  • Need to approve USDC contracts")
                if not all(ctf_approvals.values()):
                    print("  • Need to approve CTF contracts")
            print("\nRun: python complete_approvals.py")
    
    def watch_balances(self, interval=30):
        """Continuously monitor balances"""
        print(f"Monitoring balances every {interval} seconds")
        print("Press Ctrl+C to stop\n")
        
        try:
            while True:
                os.system('clear' if os.name == 'posix' else 'cls')
                self.display_balances()
                time.sleep(interval)
        except KeyboardInterrupt:
            print("\n\nMonitoring stopped")

def main():
    parser = argparse.ArgumentParser(description='Check Polymarket balances')
    parser.add_argument('--json', action='store_true', help='Output as JSON')
    parser.add_argument('--watch', action='store_true', help='Continuous monitoring')
    parser.add_argument('--interval', type=int, default=30, 
                       help='Watch interval in seconds (default: 30)')
    
    args = parser.parse_args()
    
    try:
        checker = BalanceChecker()
        
        if args.watch:
            checker.watch_balances(interval=args.interval)
        else:
            checker.display_balances(json_output=args.json)
            
    except Exception as e:
        print(f"Error: {e}")
        import traceback
        traceback.print_exc()

if __name__ == "__main__":
    main()

