package proxy

import (
	"github.com/lightningnetwork/lnd/lnwallet"
	"github.com/roasbeef/btcd/wire"
	"github.com/roasbeef/btcd/btcec"
	"errors"
	"log"
	"net/http"
	"bytes"
	"encoding/binary"
	"bufio"
	"github.com/gorilla/websocket"
	"net"
)

type LightningProxy struct {
	connected bool
	rx chan []byte
	rootKeyRaw []byte
}

var noconn = errors.New("not connected")

//// SignDescriptor houses the necessary information required to successfully sign
//// a given output. This struct is used by the Signer interface in order to gain
//// access to critical data needed to generate a valid signature.
//type SignDescriptor struct {
//	// Pubkey is the public key to which the signature should be generated
//	// over. The Signer should then generate a signature with the private
//	// key corresponding to this public key.
//	PubKey *btcec.PublicKey
//
//	// PrivateTweak is a scalar value that should be added to the private
//	// key corresponding to the above public key to obtain the private key
//	// to be used to sign this input. This value is typically a leaf node
//	// from the revocation tree.
//	//
//	// NOTE: If this value is nil, then the input can be signed using only
//	// the above public key.
//	PrivateTweak []byte
//
//	// WitnessScript is the full script required to properly redeem the
//	// output. This field will only be populated if a p2wsh or a p2sh
//	// output is being signed.
//	WitnessScript []byte
//
//	// Output is the target output which should be signed. The PkScript and
//	// Value fields within the output should be properly populated,
//	// otherwise an invalid signature may be generated.
//	Output *wire.TxOut
//
//	// HashType is the target sighash type that should be used when
//	// generating the final sighash, and signature.
//	HashType txscript.SigHashType
//
//	// SigHashes is the pre-computed sighash midstate to be used when
//	// generating the final sighash for signing.
//	SigHashes *txscript.TxSigHashes
//
//	// InputIndex is the target input within the transaction that should be
//	// signed.
//	InputIndex int
//}


func serialize(s *lnwallet.SignDescriptor) ([]byte) {
	var buf bytes.Buffer
	writer := bufio.NewWriter(&buf)

	if s.PubKey != nil {
		buf.Write(s.PubKey.SerializeCompressed())
	} else {
		for i:=0; i<33; i++ {
			buf.WriteByte(0)
		}
	}
	buf.Write(s.PrivateTweak)
	buf.Write(s.WitnessScript)

	binary.Write(writer, binary.BigEndian, s.Output.Value)
	writer.Flush()

	buf.Write(s.Output.PkScript)

	binary.Write(writer, binary.BigEndian, s.HashType)
	writer.Flush()

	buf.Write(s.SigHashes.HashPrevOuts[:])
	buf.Write(s.SigHashes.HashSequence[:])
	buf.Write(s.SigHashes.HashOutputs[:])
	return buf.Bytes()
}

func serializetx(tx *wire.MsgTx, signDesc *lnwallet.SignDescriptor) ([]byte) {
		var byts []byte
		var buf bytes.Buffer
		tx.Serialize(&buf)
		byts = append(byts, buf.Bytes()...)
		byts = append(byts, serialize(signDesc)...)
		return byts
}

func (b *LightningProxy) SignOutputRaw(tx *wire.MsgTx, signDesc *lnwallet.SignDescriptor) ([]byte, error) {
		byts := []byte("SignOutputRaw")
		byts = append(byts, serializetx(tx, signDesc)...)
		b.rx <- byts
		msg := <-b.rx
		return msg, nil
}

func (b *LightningProxy) ComputeInputScript(tx *wire.MsgTx,
	signDesc *lnwallet.SignDescriptor) (*lnwallet.InputScript, error) {
		byts := []byte("ComputeInputScript")
		byts = append(byts, serializetx(tx, signDesc)...)
		b.rx <- byts
		msg := <-b.rx
		var numWitnessArray = binary.BigEndian.Uint16(msg[:2])
		var witnesses = make([][]byte, numWitnessArray)
		var reader = bufio.NewReader(bytes.NewBuffer(msg[2:]))
		for i:= 0; i<int(numWitnessArray); i++ {
			var numWitnesses uint16
			err := binary.Read(reader, binary.BigEndian, &numWitnesses)
			var these = make([]byte, numWitnesses)
			num, err := reader.Read(these)
			if err != nil || num < 1 {
				log.Fatal("noooo, ran out of bytes while reading in ComputeInputScript")
				return nil, errors.New("ran out of bytes in ComputeInputScript")
			}
			witnesses[i] = these
		}
		var scriptBuf bytes.Buffer
		writer := bufio.NewWriter(&scriptBuf)
		for {
			bajt, err := reader.ReadByte()
			if err != nil {
				break
			}
			writer.WriteByte(bajt)
		}
		writer.Flush()
		var inputScript = lnwallet.InputScript{ Witness: witnesses, ScriptSig: scriptBuf.Bytes() }
		return &inputScript, nil
}

var _ lnwallet.Signer = (*LightningProxy)(nil)

var upgrader = websocket.Upgrader{
	ReadBufferSize:	1024,
	WriteBufferSize: 1024,
}

func writePump(b *LightningProxy, conn *websocket.Conn) {
	for {
		msg := <-b.rx
		err := conn.WriteMessage(websocket.BinaryMessage, msg)
		if err != nil {
			b.connected = false
			log.Fatal(err)
			break
		}
	}
}

func readPump(b *LightningProxy, conn *websocket.Conn) {
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			b.connected = false
			log.Fatal(err)
			break
		}
		b.rx <- message
	}
}

func loop(b *LightningProxy) {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if b.connected {
			log.Fatal("already connected")
			return
		}
		b.connected = true
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println(err)
			return
		}
		err = conn.WriteMessage(websocket.BinaryMessage, b.rootKeyRaw)
		if err != nil {
			log.Println(err)
			return
		}
		// Allow collection of memory referenced by the caller by doing all work in
		// new goroutines.
		go writePump(b, conn)
		go readPump(b, conn)
	})
	listener, err := net.Listen("tcp", "0.0.0.0:19283")
	if err != nil {
			panic(err)
	}

	log.Println("Using port:", listener.Addr().(*net.TCPAddr).Port)
	for {
		err := http.Serve(listener, nil)
		if err != nil {
			log.Fatal("ListenAndServe: ", err)
			return
		}
	}
}

func New() (*LightningProxy) {
	tx := make(chan []byte)
	str := &LightningProxy{rx: tx, connected: false}
	go loop(str)
	return str
}

func (b *LightningProxy) SetWallet(w *lnwallet.LightningWallet) {
	var root *btcec.PrivateKey
  var err error
  root, err = w.FetchRootKey()
	if err != nil {
		panic(err)
	}
	b.rootKeyRaw = root.Serialize()
}
