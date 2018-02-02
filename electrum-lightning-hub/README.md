```
# You need Go 1.8+ but Ubuntu 16.04 has only Go 1.6
sudo add-apt-repository ppa:longsleep/golang-backports
sudo apt-get update
sudo apt-get install golang-go

# Glide dependency manager
go get -u github.com/Masterminds/glide

# It is recommended that $GOPATH is set to a directory in your home directory such as ~/go to avoid write permission issues. It is also recommended to add $GOPATH/bin to your PATH at this point.

mkdir -p 
git clone https://github.com/Roasbeef/btcd.git $GOPATH/src/github.com/roasbeef/btcd 
cd $GOPATH/src/github.com/roasbeef/btcd
glide install
go install . ./cmd/...

# Link btcd directory to right storage mount
ln -s /ssd ~/.btcd

# Write btcd config
cat <<EOF > ~/.btcd/btcd.conf
rpcuser=youruser
rpcpass=SomeDecentp4ssw0rd
testnet=1
txindex=1
addrindex=1
rpclisten=127.0.0.1
EOF

sudo apt install screen

screen btcd

# Detach using Control-A Control-D

cd $GOPATH/src/github
mkdir lightningnetwork
cd lightningnetwork
git clone https://github.com/ysangkok/lnd

cd

sudo apt install unzip
wget https://github.com/google/protobuf/releases/download/v3.5.0/protoc-3.5.0-linux-x86_64.zip
unzip protoc*.zip

mv bin/protoc go/bin/

sudo pip3 install grpcio-tools

git clone https://github.com/ysangkok/electrum-lightning-test
cd electrum-lightning-test/electrum-lightning-hub
# The following script uses the Protobuf files in the lnd folder
./protoc_lightning.sh

cd $GOPATH/src/github.com/lightningnetwork/lnd
glide install
go get -u github.com/golang/protobuf/protoc-gen-go
go get -u github.com/grpc-ecosystem/grpc-gateway/protoc-gen-grpc-gateway
./protoc_electrum.sh
go install . ./cmd/...

cd $HOME/electrum-lightning-test/electrum-lightning-hub
sudo pip3 install -r requirements.txt
PYTHONPATH=lib/ln python3 repeater_and_rpc.py
```
