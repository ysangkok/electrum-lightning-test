FROM elt-vanilla-lnd
RUN go get github.com/gorilla/websocket
ADD patch .
RUN patch -p1 < patch
ADD lightning-proxy vendor/github.com/lightningnetwork/lnd/lightning-proxy/.
RUN rm htlcswitch/test_utils.go
RUN go install . ./cmd/...
