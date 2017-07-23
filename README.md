setup:

* create simnet btcwallet in config-btcwallet (do not overwrite the config!), but it's address in config-btcd1/btcd.conf
* use openssl to generate key.pem and cert.pem in config-electrumx

test:
```
docker exec -it electrumlightningtest_btcd1_1 /go/bin/btcctl --notls generate 100
docker exec -it electrumlightningtest_btcd1_1 /go/bin/btcctl --wallet --notls --rpcserver btcw getbalance
docker exec -it electrumlightningtest_btcd1_1 /go/bin/btcctl --notls generate 1
# expect 100


check that eleclnd is synced to chain:
docker exec -it electrumlightningtest_eleclnd_1 /go/bin/lncli getinfo
```
