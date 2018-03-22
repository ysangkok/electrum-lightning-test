#!/bin/bash -xe
cd electrum-lightning-hub
if [ ! -d ../venv ]; then
	python3.6 -m venv ../venv
fi
../venv/bin/pip install -r requirements.txt
if [ ! -d ~/go/src/github.com/lightningnetwork/lnd ]; then
	mkdir -p ~/go/src/github.com/lightningnetwork
	cd ~/go/src/github.com/lightningnetwork
	git clone https://github.com/ysangkok/lnd
	cd -
fi
(cd ~/go/src/github.com/lightningnetwork/lnd && git fetch --all && git reset --hard origin/electrum && git checkout -f origin/electrum)
if [ ! -f ~/go/bin/protoc ]; then
	mkdir -p ~/go || true
	cd ~/go
	wget https://github.com/google/protobuf/releases/download/v3.5.1/protoc-3.5.1-linux-x86_64.zip
	unzip protoc*
	mv include ..
	cd -
fi
./protoc_lightning.sh
rm -vf screenlog.*
screen -L -S lightning-hub -d -m env PYTHONPATH=lib/ln ../venv/bin/python repeater_and_rpc.py
screen -ls
tail -f screenlog.0 &
sleep 10
if [ ! -d ../electrum ]; then
  git clone https://github.com/spesmilo/electrum.git ../electrum
  cd ../electrum
  git checkout lightning
else
	cd ../electrum
fi
git fetch --all
git reset --hard origin/lightning
git clean -d -x -f
if [ -f contrib/deterministic-build/requirements.txt ]; then
  ../venv/bin/pip install -r contrib/deterministic-build/requirements.txt
else
  ../venv/bin/pip install -r contrib/requirements.txt
fi
(cd ~/go/src/github.com/lightningnetwork/lnd && ./protoc_electrum.sh)
(cd ~/go/src/github.com/lightningnetwork/lnd && go get -u github.com/golang/dep/cmd/dep && ~/go/bin/dep ensure && go install . ./cmd/...)
./protoc_lightning.sh
ELECDIR1=$(mktemp)
ELECDIR2=$(mktemp)
for ELECDIR in $ELECDIR1 $ELECDIR2; do
  rm $ELECDIR
  ELECDIR=$ELECDIR ../create.expect
  PYTHONPATH=lib/ln ../venv/bin/python ./electrum --testnet daemon start -D $ELECDIR
	sleep 1
  PYTHONPATH=lib/ln ../venv/bin/python ./electrum --testnet daemon load_wallet -D $ELECDIR
done
sleep 5
set +x

function retryuntilnonnull {
  while true; do
    if [[ "$3" == "" ]]; then
      lightningargs="[]"
    else
      lightningargs="$3"
    fi
    OUT="$(echo $lightningargs | ../venv/bin/python ./electrum --testnet lightning $1 -D $2 --lightningargs -)"
    CODE="$(echo $OUT | jq .returncode || true)"
    if [[ $CODE == "null" ]]; then
      # returncode is only there on error (see lncli_endpoint.py)
      echo "$OUT"
      break
    fi
    echo "$OUT" | jq .stderr >> /dev/stderr
    sleep 10
  done
}

for ELECDIR in $ELECDIR1 $ELECDIR2; do
	retryuntilnonnull getinfo $ELECDIR
done
set -x

NODE1PUBK=$(PYTHONPATH=lib/ln ../venv/bin/python electrum --testnet lightning getinfo -D $ELECDIR1 | jq -r .identity_pubkey)
NODE2PUBK=$(PYTHONPATH=lib/ln ../venv/bin/python electrum --testnet lightning getinfo -D $ELECDIR2 | jq -r .identity_pubkey)
#retryuntilnonnull connect $ELECDIR2 "["\""$NODE1PUBK@127.0.0.1"\""]"

NODE1ADDR=$(echo "["\""p2wkh"\""]" | ../venv/bin/python electrum --testnet lightning newaddress -D $ELECDIR1 --lightningargs - | jq -r .address)
if [[ $NODE1ADDR == "null" ]]; then
	echo "test.sh: Could not get address"
	exit 1
fi
if [ ! -d ../upstreamelectrum ]; then
	git clone https://github.com/spesmilo/electrum ../upstreamelectrum
fi
cd ../upstreamelectrum
git pull
if [ -f contrib/deterministic-build/requirements.txt ]; then
  ../venv/bin/pip install -r contrib/deterministic-build/requirements.txt
fi
../venv/bin/python electrum --testnet daemon start
sleep 1
../venv/bin/python electrum --testnet daemon load_wallet
while [[ $(../venv/bin/python electrum --testnet is_synchronized) != "true" ]]; do
  sleep 1
done
../venv/bin/python electrum --testnet daemon status # TODO check if server_height == local_height
../venv/bin/python electrum --testnet getbalance
../venv/bin/python electrum --testnet listaddresses
../venv/bin/python electrum --testnet payto $NODE1ADDR 0.01 | ../venv/bin/python electrum --testnet broadcast -
sleep 1
../venv/bin/python electrum --testnet daemon stop
cd -

sleep 60

for ELECDIR in $ELECDIR1 $ELECDIR2; do
  PYTHONPATH=lib/ln ../venv/bin/python ./electrum --testnet daemon status -D $ELECDIR
  PYTHONPATH=lib/ln ../venv/bin/python ./electrum --testnet getbalance -D $ELECDIR
done

# from json to sh:
# for i in $(./electrum --testnet listaddresses | jq -r '@sh "echo \(.)"' | sh); do echo $i; done
echo $NODE2PUBK 100000 | python3 -c 'import json, sys; print(json.dumps(sys.stdin.read().rstrip().split(" ")))' | bash -c "time ../venv/bin/python electrum --testnet lightning openchannel -D $ELECDIR1 --lightningargs -"
#screen -X -S lightning-hub quit
#ps aux | grep lnd
#(
#  cd ../electrum-lightning-hub
#  screen -L -S lightning-hub -d -m env PYTHONPATH=lib/ln ../venv/bin/python repeater_and_rpc.py
#  sleep 5
#)
set +x
while true; do
  OUT="$(PYTHONPATH=lib/ln ../venv/bin/python electrum --testnet lightning listchannels -D $ELECDIR1)"
  CODE="$(echo $OUT | jq -r '.channels|length' || true)"
  if [[ $CODE == "1" ]]; then
		echo "$OUT"
    break
	fi
	sleep 60
done
set -x

PAYREQ=$(retryuntilnonnull addinvoice $ELECDIR2 "["\""--amt=8192"\""]" | jq -r .pay_req)
echo "["\""--pay_req=$PAYREQ"\""]" | ../venv/bin/python electrum --testnet lightning sendpayment -D $ELECDIR1 --lightningargs -

PYTHONPATH=lib/ln ../venv/bin/python electrum --testnet lightning listchannels -D $ELECDIR1
PYTHONPATH=lib/ln ../venv/bin/python electrum --testnet lightning listchannels -D $ELECDIR2

for ELECDIR in $ELECDIR1 $ELECDIR2; do
  PYTHONPATH=lib/ln ../venv/bin/python ./electrum --testnet daemon status -D $ELECDIR
done

PYTHONPATH=lib/ln ../venv/bin/python electrum --testnet lightning getinfo -D $ELECDIR1

for ELECDIR in $ELECDIR1 $ELECDIR2; do
  PYTHONPATH=lib/ln ../venv/bin/python ./electrum --testnet daemon stop -D $ELECDIR
done
screen -X -S lightning-hub quit
cd ../electrum-lightning-hub
kill %1
rm screenlog.*
