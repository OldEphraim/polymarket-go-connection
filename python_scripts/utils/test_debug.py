# Create a test file: test_debug.py
import sys
import os
sys.path.append(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from utils.orderbook_analyzer import OrderbookAnalyzer

analyzer = OrderbookAnalyzer()
token_id = "2942567299640587090091139148545794488615196042290090815333947905974415750919"

print("Fetching orderbook...")
orderbook = analyzer.fetch_orderbook(token_id)
print(f"Got orderbook with {len(orderbook.get('bids', []))} bids and {len(orderbook.get('asks', []))} asks")

print("Analyzing spread...")
spread_info = analyzer.analyze_spread(orderbook)
print(f"Spread info keys: {spread_info.keys()}")

print("Simulating buy...")
sim = analyzer.simulate_buy_fill(orderbook, 100, None)
print(f"Simulation result: fillable={sim.get('fillable')}, reason={sim.get('reason')}")
