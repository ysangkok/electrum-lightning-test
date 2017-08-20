#!/bin/sh -ex
grep ^miningaddr config-btcd1/btcd.conf
sed -i.bak -re "s/miningaddr=.*/miningaddr=$(docker exec -it electrumlightningtest_btcd2_1 /go/bin/btcctl --wallet --notls --rpcserver btcw getnewaddress | tr -d '[:space:]' )/" config-btcd1/btcd.conf
