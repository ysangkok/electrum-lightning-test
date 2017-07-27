#!/bin/bash
VANILLAIP=$(docker exec -it electrumlightningtest_vanillalnd_1 ip addr | grep inet | grep -v 127.0 | cut -d" " -f6 | cut -d/ -f1)
VANILLAKEY=$(docker exec -it electrumlightningtest_vanillalnd_1 /go/bin/lncli getinfo | jq -r .identity_pubkey)
docker exec -it electrumlightningtest_eleclnd_1 /go/bin/lncli connect ${VANILLAKEY}@${VANILLAIP} > /dev/null
echo $VANILLAKEY
