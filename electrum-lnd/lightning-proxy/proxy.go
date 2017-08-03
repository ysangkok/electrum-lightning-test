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
	"fmt"
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

	var err error
	if s.PubKey != nil {
		_, err = buf.Write(s.PubKey.SerializeCompressed())
		if err != nil { panic("conv0 failed"); }
	} else {
		for i:=0; i<33; i++ {
			buf.WriteByte(0)
		}
	}
	if (binary.Write(writer, binary.BigEndian, int16(len(s.SingleTweak))) != nil) { panic("conv1 failed"); }
	writer.Flush()

	buf.Write(s.SingleTweak)

	if s.DoubleTweak != nil {
		bajts := s.DoubleTweak.Serialize()
		if len(bajts) != 32 {
			panic("weird length")
		}
		_, err = buf.Write(bajts)
		if err != nil { panic("conv1.5 failed"); }
	} else {
		for i:=0; i<32; i++ {
			buf.WriteByte(0)
		}
	}

	if (binary.Write(writer, binary.BigEndian, int16(len(s.WitnessScript))) != nil) { panic("conv2 failed"); }
	writer.Flush()

	buf.Write(s.WitnessScript)

	if (binary.Write(writer, binary.BigEndian, s.Output.Value) != nil) { panic("conv3 failed"); }
	writer.Flush()

	if (binary.Write(writer, binary.BigEndian, int16(len(s.Output.PkScript))) != nil) { panic("conv4 failed"); }
	writer.Flush()

	buf.Write(s.Output.PkScript)

	if (binary.Write(writer, binary.BigEndian, s.HashType) != nil) { panic("conv5 failed"); }
	writer.Flush()

	_, err = buf.Write(s.SigHashes.HashPrevOuts[:])
	if err != nil { panic("conv6 failed"); }
	_, err = buf.Write(s.SigHashes.HashSequence[:])
	if err != nil { panic("conv7 failed"); }
	_, err = buf.Write(s.SigHashes.HashOutputs[:])
	if err != nil { panic("conv8 failed"); }

	if (binary.Write(writer, binary.BigEndian, int64(s.InputIndex)) != nil) { panic("conv9 failed"); }
	writer.Flush()

	bajts := buf.Bytes()

	var lenbuf bytes.Buffer
	lenwriter := bufio.NewWriter(&lenbuf)
	err = binary.Write(lenwriter, binary.BigEndian, int16(len(bajts)))
	if err != nil { panic("conv9 failed"); }
	lenwriter.Flush()

	return append(lenbuf.Bytes(), bajts...)
}

func serializetx(tx *wire.MsgTx, signDesc *lnwallet.SignDescriptor) ([]byte) {
		var byts []byte
		var buf bytes.Buffer
		if tx.Serialize(&buf) != nil {
			panic("could not serialize transaction")
		}
		bajts := buf.Bytes()

		var lenbuf bytes.Buffer
		lenwriter := bufio.NewWriter(&lenbuf)
		err := binary.Write(lenwriter, binary.BigEndian, int16(len(bajts)))
		if err != nil {
			panic("could not write tx length")
		}
		lenwriter.Flush()

		part1 := lenbuf.Bytes()
		part2 := buf.Bytes()
		part3 := serialize(signDesc)
		byts = append(byts, part1...)
		byts = append(byts, part2...)
		byts = append(byts, part3...)
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
			var sizeWitness uint16
			err := binary.Read(reader, binary.BigEndian, &sizeWitness)
			if err != nil {
				log.Fatal("could not read number of witnesses")
				return nil, errors.New("could not read number of witnesses")
			}
			var these = make([]byte, sizeWitness)
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
		scriptSigBytes := scriptBuf.Bytes()
		if len(scriptSigBytes) == 0 {
			scriptSigBytes = nil
		}
		var inputScript = lnwallet.InputScript{ Witness: witnesses, ScriptSig: scriptSigBytes }
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
