#!/bin/bash -ex
NEWADDRESS=$(docker exec -it electrumlightningtest_${1}_1 /go/bin/lncli newaddress np2wkh | jq -r .address)
docker exec -it electrumlightningtest_btcd1_1 /go/bin/btcctl --wallet --notls --rpcserver btcw walletpassphrase donkey 100
docker exec -it electrumlightningtest_btcd1_1 /go/bin/btcctl --wallet --notls --rpcserver btcw sendtoaddress $NEWADDRESS 49
