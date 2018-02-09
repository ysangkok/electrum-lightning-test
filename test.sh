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
git pull
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
rm $ELECDIR1
ELECDIR=$ELECDIR1 ../create.expect
PYTHONPATH=lib/ln ../venv/bin/python ./electrum --testnet daemon start -D $ELECDIR1
PYTHONPATH=lib/ln ../venv/bin/python ./electrum --testnet daemon load_wallet -D $ELECDIR1
sleep 5
set +x
for i in $(seq 0 100); do
  OUT="$(PYTHONPATH=lib/ln ../venv/bin/python ./electrum --testnet lightning getinfo -D $ELECDIR1)"
  CODE="$(echo $OUT | jq .returncode)"
  if [[ $CODE == "null" ]]; then
    # returncode is only there on error (see lncli_endpoint.py)
    echo "$OUT"
    break
  fi
  echo "$OUT"
	sleep 1
done
set -x
PYTHONPATH=lib/ln ../venv/bin/python ./electrum --testnet daemon stop -D $ELECDIR1
screen -X -S lightning-hub quit
