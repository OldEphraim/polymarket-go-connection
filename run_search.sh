#!/bin/bash
cd python_scripts
source venv/bin/activate
python utils/search_polymarket.py "$@"
