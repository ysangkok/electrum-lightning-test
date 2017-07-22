FROM elt-vanilla-lnd
RUN apt-get update && apt-get install -y patch
RUN go get github.com/gorilla/websocket
ADD patch .
RUN patch -p1 < patch
ADD lightning-proxy vendor/github.com/lightningnetwork/lnd/lightning-proxy/.
RUN go install . ./cmd/...
