FROM golang:1.8
RUN apt update && apt install -y patch
RUN go get -u github.com/Masterminds/glide
RUN git clone https://github.com/lightningnetwork/lnd.git $GOPATH/src/github.com/lightningnetwork/lnd
WORKDIR $GOPATH/src/github.com/lightningnetwork/lnd
RUN git checkout b1205e71ecb46f9a5fc4bed068436c8a5f55c487 # aug 3 2017
ADD noTLS.patch .
RUN patch -p1 < noTLS.patch
ADD dumpInputSig.patch .
RUN patch -p1 < dumpInputSig.patch
RUN glide install
RUN go install . ./cmd/...
