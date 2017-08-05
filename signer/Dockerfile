FROM golang:1.8
RUN go get -u github.com/Masterminds/glide
WORKDIR $GOPATH/src/github.com/ysangkok/electrum-lightning-test
ADD . .
RUN glide install
RUN go test
