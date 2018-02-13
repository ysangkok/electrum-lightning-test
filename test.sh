#!/bin/bash -xe
cd electrum-lightning-hub
if [ ! -d ../venv ]; then
	python3.6 -m venv ../venv
fi
../venv/bin/pip install -r requirements.txt
./protoc_lightning.sh
screen -S lightning-hub -d -m env PYTHONPATH=lib/ln ../venv/bin/python repeater_and_rpc.py
screen -ls
sleep 10
if [ ! -d ../electrum ]; then
  git clone https://github.com/spesmilo/electrum.git ../electrum
  cd ../electrum
  git checkout lightning
else
	cd ../electrum
fi
git reset --hard
git clean -d -x -f
git checkout lightning
git pull origin lightning
if [ -f contrib/deterministic-build/requirements.txt ]; then
  ../venv/bin/pip install -r contrib/deterministic-build/requirements.txt
else
  ../venv/bin/pip install -r contrib/requirements.txt
fi
(cd ~/go/src/github.com/lightningnetwork/lnd && git pull)
(cd ~/go/src/github.com/lightningnetwork/lnd && ./protoc_electrum.sh)
(cd ~/go/src/github.com/lightningnetwork/lnd && go get -u github.com/Masterminds/glide && ~/go/bin/glide install && go install . ./cmd/...)
rm -rf ~/.electrum/testnet
./protoc_lightning.sh
ELECDIR1=$(mktemp)
ELECDIR2=$(mktemp)
for ELECDIR in $ELECDIR1 $ELECDIR2; do
  rm $ELECDIR
  ELECDIR=$ELECDIR ../create.expect
  PYTHONPATH=lib/ln ../venv/bin/python ./electrum --testnet daemon start -v -D $ELECDIR 
  PYTHONPATH=lib/ln ../venv/bin/python ./electrum --testnet daemon load_wallet -D $ELECDIR
done
sleep 5
set +x
for ELECDIR in $ELECDIR1 $ELECDIR2; do
  for i in $(seq 0 100); do
    OUT="$(PYTHONPATH=lib/ln ../venv/bin/python ./electrum --testnet lightning getinfo -D $ELECDIR)"
    CODE="$(echo $OUT | jq .returncode || true)"
    if [[ $CODE == "null" ]]; then
      # returncode is only there on error (see lncli_endpoint.py)
      echo "$OUT"
      break
    fi
    echo "$OUT"
  	sleep 2
  done
done
set -x

NODE1PUBK=$(PYTHONPATH=lib/ln ../venv/bin/python electrum --testnet lightning getinfo -D $ELECDIR1 | jq -r .identity_pubkey)
NODE2PUBK=$(PYTHONPATH=lib/ln ../venv/bin/python electrum --testnet lightning getinfo -D $ELECDIR2 | jq -r .identity_pubkey)
echo "["\""$NODE1PUBK@127.0.0.1"\""]" | ../venv/bin/python electrum --testnet lightning connect -D $ELECDIR2 --lightningargs -

NODE1ADDR=$(echo "["\""np2wkh"\""]" | ../venv/bin/python electrum --testnet lightning newaddress -D $ELECDIR1 --lightningargs - | jq -r .address)
cd ../upstreamelectrum
../venv/bin/python electrum --testnet daemon start
sleep 3
../venv/bin/python electrum --testnet payto $NODE1ADDR 0.01 | ../venv/bin/python electrum broadcast
../venv/bin/python electrum --testnet daemon stop

sleep 600

echo $NODE2PUBK 10000 | python3 -c 'import json, sys; print(json.dumps(sys.stdin.read().rstrip().split(" ")))' | ../venv/bin/python electrum --testnet lightning openchannel -D $ELECDIR1 --lightningargs -
PYTHONPATH=lib/ln ../venv/bin/python electrum --testnet lightning listchannels -D $ELECDIR1
sleep 600
PYTHONPATH=lib/ln ../venv/bin/python electrum --testnet lightning listchannels -D $ELECDIR1

for ELECDIR in $ELECDIR1 $ELECDIR2; do
  PYTHONPATH=lib/ln ../venv/bin/python ./electrum --testnet daemon stop -D $ELECDIR
done
screen -X -S lightning-hub quit
