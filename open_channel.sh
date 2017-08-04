#!/bin/sh
VANILLAKEY=$(./connect_lnd_nodes.sh)
docker exec -it electrumlightningtest_eleclnd_1 /go/bin/lncli openchannel --node_key=$VANILLAKEY --local_amt=10000
