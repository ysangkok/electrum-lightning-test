FROM elt-vanilla-lnd
RUN apt-get update && apt-get install -y patch
ADD patch .
RUN patch -p1 < patch
RUN go get github.com/gorilla/websocket
ADD lightning-proxy vendor/github.com/lightningnetwork/lnd/lightning-proxy/.
RUN go install . ./cmd/...
