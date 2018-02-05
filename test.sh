#!/bin/bash -xe
cd electrum-lightning-hub
PYTHONPATH=../electrum/lib/ln screen -S lightning-hub -d -m python3.6 repeater_and_rpc.py
screen -ls
sleep 10
if [ ! -d ../electrum ]; then
  git clone https://github.com/spesmilo/electrum.git ../electrum
  cd ../electrum
  git checkout lightning
else
	cd ../electrum
fi
if [ ! -d ../venv ]; then
	python3.6 -m venv ../venv
fi
../venv/bin/pip install -r contrib/deterministic-build/requirements.txt
rm -rf ~/.electrum/testnet
PYTHONPATH=lib/ln ../create.expect
PYTHONPATH=lib/ln ../venv/bin/pip/python ./electrum --testnet daemon start
PYTHONPATH=lib/ln ../venv/bin/pip/python ./electrum --testnet daemon load_wallet
sleep 5
while true; do
  OUT="$(PYTHONPATH=lib/ln ../venv/bin/pip/python ./electrum --testnet lightning getinfo)"
  CODE="$(echo $OUT | jq .returncode)"
  if [[ $CODE == "null" ]]; then
    # returncode is only there on error (see lncli_endpoint.py)
    echo "$OUT"
    break
  fi
  echo "$OUT"
done
PYTHONPATH=lib/ln ../venv/bin/pip/python ./electrum --testnet daemon stop
screen -X -S lightning-hub quit
