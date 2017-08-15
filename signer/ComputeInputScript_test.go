package test

import "testing"
//# btcd master branch on aug 4, 2017
import "github.com/roasbeef/btcd/chaincfg"
import "github.com/roasbeef/btcd/txscript"
import "github.com/roasbeef/btcd/chaincfg/chainhash"
import "github.com/roasbeef/btcutil"
import "github.com/roasbeef/btcd/btcec"
import "github.com/roasbeef/btcutil/hdkeychain"
import "fmt"
import "bytes"
import "encoding/binary"
import "errors"
import "io"
import "math"
import "math/big"
import "crypto/sha256"
import "time"
import "reflect"
import "golang.org/x/crypto/hkdf"

const (
	// MaxVarIntPayload is the maximum payload size for a variable length integer.
	MaxVarIntPayload = 9

	// binaryFreeListMaxItems is the number of buffers to keep in the free
	// list to use for binary serialization and deserialization.
	binaryFreeListMaxItems = 1024
)

var (
	// littleEndian is a convenience variable since binary.LittleEndian is
	// quite long.
	littleEndian = binary.LittleEndian

	// bigEndian is a convenience variable since binary.BigEndian is quite
	// long.
	bigEndian = binary.BigEndian
)

// PutUint64 serializes the provided uint64 using the given byte order into a
// buffer from the free list and writes the resulting eight bytes to the given
// writer.
func (l binaryFreeList) PutUint64(w io.Writer, byteOrder binary.ByteOrder, val uint64) error {
	buf := l.Borrow()[:8]
	byteOrder.PutUint64(buf, val)
	_, err := w.Write(buf)
	l.Return(buf)
	return err
}

// PutUint16 serializes the provided uint16 using the given byte order into a
// buffer from the free list and writes the resulting two bytes to the given
// writer.
func (l binaryFreeList) PutUint16(w io.Writer, byteOrder binary.ByteOrder, val uint16) error {
	buf := l.Borrow()[:2]
	byteOrder.PutUint16(buf, val)
	_, err := w.Write(buf)
	l.Return(buf)
	return err
}

// PutUint32 serializes the provided uint32 using the given byte order into a
// buffer from the free list and writes the resulting four bytes to the given
// writer.
func (l binaryFreeList) PutUint32(w io.Writer, byteOrder binary.ByteOrder, val uint32) error {
	buf := l.Borrow()[:4]
	byteOrder.PutUint32(buf, val)
	_, err := w.Write(buf)
	l.Return(buf)
	return err
}

// PutUint8 copies the provided uint8 into a buffer from the free list and
// writes the resulting byte to the given writer.
func (l binaryFreeList) PutUint8(w io.Writer, val uint8) error {
	buf := l.Borrow()[:1]
	buf[0] = val
	_, err := w.Write(buf)
	l.Return(buf)
	return err
}

// Borrow returns a byte slice from the free list with a length of 8.  A new
// buffer is allocated if there are not any available on the free list.
func (l binaryFreeList) Borrow() []byte {
	var buf []byte
	select {
	case buf = <-l:
	default:
		buf = make([]byte, 8)
	}
	return buf[:8]
}

// Uint32 reads four bytes from the provided reader using a buffer from the
// free list, converts it to a number using the provided byte order, and returns
// the resulting uint32.
func (l binaryFreeList) Uint32(r io.Reader, byteOrder binary.ByteOrder) (uint32, error) {
	buf := l.Borrow()[:4]
	if _, err := io.ReadFull(r, buf); err != nil {
		l.Return(buf)
		return 0, err
	}
	rv := byteOrder.Uint32(buf)
	l.Return(buf)
	return rv, nil
}

// Return puts the provided byte slice back on the free list.  The buffer MUST
// have been obtained via the Borrow function and therefore have a cap of 8.
func (l binaryFreeList) Return(buf []byte) {
	select {
	case l <- buf:
	default:
		// Let it go to the garbage collector.
	}
}

// binaryFreeList defines a concurrent safe free list of byte slices (up to the
// maximum number defined by the binaryFreeListMaxItems constant) that have a
// cap of 8 (thus it supports up to a uint64).  It is used to provide temporary
// buffers for serializing and deserializing primitive numbers to and from their
// binary encoding in order to greatly reduce the number of allocations
// required.
//
// For convenience, functions are provided for each of the primitive unsigned
// integers that automatically obtain a buffer from the free list, perform the
// necessary binary conversion, read from or write to the given io.Reader or
// io.Writer, and return the buffer to the free list.
type binaryFreeList chan []byte

// binarySerializer provides a free list of buffers to use for serializing and
// deserializing primitive integer values to and from io.Readers and io.Writers.
var binarySerializer binaryFreeList = make(chan []byte, binaryFreeListMaxItems)

// WriteVarInt serializes val to w using a variable number of bytes depending
// on its value.
func WriteVarInt(w io.Writer, pver uint32, val uint64) error {
	if val < 0xfd {
		return binarySerializer.PutUint8(w, uint8(val))
	}

	if val <= math.MaxUint16 {
		err := binarySerializer.PutUint8(w, 0xfd)
		if err != nil {
			return err
		}
		return binarySerializer.PutUint16(w, littleEndian, uint16(val))
	}

	if val <= math.MaxUint32 {
		err := binarySerializer.PutUint8(w, 0xfe)
		if err != nil {
			return err
		}
		return binarySerializer.PutUint32(w, littleEndian, uint32(val))
	}

	err := binarySerializer.PutUint8(w, 0xff)
	if err != nil {
		return err
	}
	return binarySerializer.PutUint64(w, littleEndian, val)
}

// parsedOpcode represents an opcode that has been parsed and includes any
// potential data associated with it.
type parsedOpcode struct {
	opcode *opcode
	data   []byte
}

// An opcode defines the information related to a txscript opcode.  opfunc, if
// present, is the function to call to perform the opcode on the script.  The
// current script is passed in as a slice with the first member being the opcode
// itself.
type opcode struct {
	value  byte
	name   string
	length int
}

// TxSigHashes houses the partial set of sighashes introduced within BIP0143.
// This partial set of sighashes may be re-used within each input across a
// transaction when validating all inputs. As a result, validation complexity
// for SigHashAll can be reduced by a polynomial factor.
type TxSigHashes struct {
	HashPrevOuts chainhash.Hash
	HashSequence chainhash.Hash
	HashOutputs  chainhash.Hash
}

// SigHashType represents hash type bits at the end of a signature.
type SigHashType uint32

// Hash type bits from the end of a signature.
const (
	SigHashOld          SigHashType = 0x0
	SigHashAll          SigHashType = 0x1
	SigHashNone         SigHashType = 0x2
	SigHashSingle       SigHashType = 0x3
	SigHashAnyOneCanPay SigHashType = 0x80

	// sigHashMask defines the number of bits of the hash type which is used
	// to identify which outputs are signed.
	sigHashMask = 0x1f
)

// OutPoint defines a bitcoin data type that is used to track previous
// transaction outputs.
type OutPoint struct {
	Hash  chainhash.Hash
	Index uint32
}

/*
const (
	OP_0                   = 0x00 // 0
	OP_DATA_20             = 0x14 // 20
	OP_DUP                 = 0x76 // 118
	OP_EQUALVERIFY         = 0x88 // 136
	OP_HASH160             = 0xa9 // 169
	OP_CHECKSIG            = 0xac // 172
)
*/
// These constants are the values of the official opcodes used on the btc wiki,
// in bitcoin core and in most if not all other references and software related
// to handling BTC scripts.
const (
	OP_0                   = 0x00 // 0
	OP_FALSE               = 0x00 // 0 - AKA OP_0
	OP_DATA_1              = 0x01 // 1
	OP_DATA_2              = 0x02 // 2
	OP_DATA_3              = 0x03 // 3
	OP_DATA_4              = 0x04 // 4
	OP_DATA_5              = 0x05 // 5
	OP_DATA_6              = 0x06 // 6
	OP_DATA_7              = 0x07 // 7
	OP_DATA_8              = 0x08 // 8
	OP_DATA_9              = 0x09 // 9
	OP_DATA_10             = 0x0a // 10
	OP_DATA_11             = 0x0b // 11
	OP_DATA_12             = 0x0c // 12
	OP_DATA_13             = 0x0d // 13
	OP_DATA_14             = 0x0e // 14
	OP_DATA_15             = 0x0f // 15
	OP_DATA_16             = 0x10 // 16
	OP_DATA_17             = 0x11 // 17
	OP_DATA_18             = 0x12 // 18
	OP_DATA_19             = 0x13 // 19
	OP_DATA_20             = 0x14 // 20
	OP_DATA_21             = 0x15 // 21
	OP_DATA_22             = 0x16 // 22
	OP_DATA_23             = 0x17 // 23
	OP_DATA_24             = 0x18 // 24
	OP_DATA_25             = 0x19 // 25
	OP_DATA_26             = 0x1a // 26
	OP_DATA_27             = 0x1b // 27
	OP_DATA_28             = 0x1c // 28
	OP_DATA_29             = 0x1d // 29
	OP_DATA_30             = 0x1e // 30
	OP_DATA_31             = 0x1f // 31
	OP_DATA_32             = 0x20 // 32
	OP_DATA_33             = 0x21 // 33
	OP_DATA_34             = 0x22 // 34
	OP_DATA_35             = 0x23 // 35
	OP_DATA_36             = 0x24 // 36
	OP_DATA_37             = 0x25 // 37
	OP_DATA_38             = 0x26 // 38
	OP_DATA_39             = 0x27 // 39
	OP_DATA_40             = 0x28 // 40
	OP_DATA_41             = 0x29 // 41
	OP_DATA_42             = 0x2a // 42
	OP_DATA_43             = 0x2b // 43
	OP_DATA_44             = 0x2c // 44
	OP_DATA_45             = 0x2d // 45
	OP_DATA_46             = 0x2e // 46
	OP_DATA_47             = 0x2f // 47
	OP_DATA_48             = 0x30 // 48
	OP_DATA_49             = 0x31 // 49
	OP_DATA_50             = 0x32 // 50
	OP_DATA_51             = 0x33 // 51
	OP_DATA_52             = 0x34 // 52
	OP_DATA_53             = 0x35 // 53
	OP_DATA_54             = 0x36 // 54
	OP_DATA_55             = 0x37 // 55
	OP_DATA_56             = 0x38 // 56
	OP_DATA_57             = 0x39 // 57
	OP_DATA_58             = 0x3a // 58
	OP_DATA_59             = 0x3b // 59
	OP_DATA_60             = 0x3c // 60
	OP_DATA_61             = 0x3d // 61
	OP_DATA_62             = 0x3e // 62
	OP_DATA_63             = 0x3f // 63
	OP_DATA_64             = 0x40 // 64
	OP_DATA_65             = 0x41 // 65
	OP_DATA_66             = 0x42 // 66
	OP_DATA_67             = 0x43 // 67
	OP_DATA_68             = 0x44 // 68
	OP_DATA_69             = 0x45 // 69
	OP_DATA_70             = 0x46 // 70
	OP_DATA_71             = 0x47 // 71
	OP_DATA_72             = 0x48 // 72
	OP_DATA_73             = 0x49 // 73
	OP_DATA_74             = 0x4a // 74
	OP_DATA_75             = 0x4b // 75
	OP_PUSHDATA1           = 0x4c // 76
	OP_PUSHDATA2           = 0x4d // 77
	OP_PUSHDATA4           = 0x4e // 78
	OP_1NEGATE             = 0x4f // 79
	OP_RESERVED            = 0x50 // 80
	OP_1                   = 0x51 // 81 - AKA OP_TRUE
	OP_TRUE                = 0x51 // 81
	OP_2                   = 0x52 // 82
	OP_3                   = 0x53 // 83
	OP_4                   = 0x54 // 84
	OP_5                   = 0x55 // 85
	OP_6                   = 0x56 // 86
	OP_7                   = 0x57 // 87
	OP_8                   = 0x58 // 88
	OP_9                   = 0x59 // 89
	OP_10                  = 0x5a // 90
	OP_11                  = 0x5b // 91
	OP_12                  = 0x5c // 92
	OP_13                  = 0x5d // 93
	OP_14                  = 0x5e // 94
	OP_15                  = 0x5f // 95
	OP_16                  = 0x60 // 96
	OP_NOP                 = 0x61 // 97
	OP_VER                 = 0x62 // 98
	OP_IF                  = 0x63 // 99
	OP_NOTIF               = 0x64 // 100
	OP_VERIF               = 0x65 // 101
	OP_VERNOTIF            = 0x66 // 102
	OP_ELSE                = 0x67 // 103
	OP_ENDIF               = 0x68 // 104
	OP_VERIFY              = 0x69 // 105
	OP_RETURN              = 0x6a // 106
	OP_TOALTSTACK          = 0x6b // 107
	OP_FROMALTSTACK        = 0x6c // 108
	OP_2DROP               = 0x6d // 109
	OP_2DUP                = 0x6e // 110
	OP_3DUP                = 0x6f // 111
	OP_2OVER               = 0x70 // 112
	OP_2ROT                = 0x71 // 113
	OP_2SWAP               = 0x72 // 114
	OP_IFDUP               = 0x73 // 115
	OP_DEPTH               = 0x74 // 116
	OP_DROP                = 0x75 // 117
	OP_DUP                 = 0x76 // 118
	OP_NIP                 = 0x77 // 119
	OP_OVER                = 0x78 // 120
	OP_PICK                = 0x79 // 121
	OP_ROLL                = 0x7a // 122
	OP_ROT                 = 0x7b // 123
	OP_SWAP                = 0x7c // 124
	OP_TUCK                = 0x7d // 125
	OP_CAT                 = 0x7e // 126
	OP_SUBSTR              = 0x7f // 127
	OP_LEFT                = 0x80 // 128
	OP_RIGHT               = 0x81 // 129
	OP_SIZE                = 0x82 // 130
	OP_INVERT              = 0x83 // 131
	OP_AND                 = 0x84 // 132
	OP_OR                  = 0x85 // 133
	OP_XOR                 = 0x86 // 134
	OP_EQUAL               = 0x87 // 135
	OP_EQUALVERIFY         = 0x88 // 136
	OP_RESERVED1           = 0x89 // 137
	OP_RESERVED2           = 0x8a // 138
	OP_1ADD                = 0x8b // 139
	OP_1SUB                = 0x8c // 140
	OP_2MUL                = 0x8d // 141
	OP_2DIV                = 0x8e // 142
	OP_NEGATE              = 0x8f // 143
	OP_ABS                 = 0x90 // 144
	OP_NOT                 = 0x91 // 145
	OP_0NOTEQUAL           = 0x92 // 146
	OP_ADD                 = 0x93 // 147
	OP_SUB                 = 0x94 // 148
	OP_MUL                 = 0x95 // 149
	OP_DIV                 = 0x96 // 150
	OP_MOD                 = 0x97 // 151
	OP_LSHIFT              = 0x98 // 152
	OP_RSHIFT              = 0x99 // 153
	OP_BOOLAND             = 0x9a // 154
	OP_BOOLOR              = 0x9b // 155
	OP_NUMEQUAL            = 0x9c // 156
	OP_NUMEQUALVERIFY      = 0x9d // 157
	OP_NUMNOTEQUAL         = 0x9e // 158
	OP_LESSTHAN            = 0x9f // 159
	OP_GREATERTHAN         = 0xa0 // 160
	OP_LESSTHANOREQUAL     = 0xa1 // 161
	OP_GREATERTHANOREQUAL  = 0xa2 // 162
	OP_MIN                 = 0xa3 // 163
	OP_MAX                 = 0xa4 // 164
	OP_WITHIN              = 0xa5 // 165
	OP_RIPEMD160           = 0xa6 // 166
	OP_SHA1                = 0xa7 // 167
	OP_SHA256              = 0xa8 // 168
	OP_HASH160             = 0xa9 // 169
	OP_HASH256             = 0xaa // 170
	OP_CODESEPARATOR       = 0xab // 171
	OP_CHECKSIG            = 0xac // 172
	OP_CHECKSIGVERIFY      = 0xad // 173
	OP_CHECKMULTISIG       = 0xae // 174
	OP_CHECKMULTISIGVERIFY = 0xaf // 175
	OP_NOP1                = 0xb0 // 176
	OP_NOP2                = 0xb1 // 177
	OP_CHECKLOCKTIMEVERIFY = 0xb1 // 177 - AKA OP_NOP2
	OP_NOP3                = 0xb2 // 178
	OP_CHECKSEQUENCEVERIFY = 0xb2 // 178 - AKA OP_NOP3
	OP_NOP4                = 0xb3 // 179
	OP_NOP5                = 0xb4 // 180
	OP_NOP6                = 0xb5 // 181
	OP_NOP7                = 0xb6 // 182
	OP_NOP8                = 0xb7 // 183
	OP_NOP9                = 0xb8 // 184
	OP_NOP10               = 0xb9 // 185
	OP_UNKNOWN186          = 0xba // 186
	OP_UNKNOWN187          = 0xbb // 187
	OP_UNKNOWN188          = 0xbc // 188
	OP_UNKNOWN189          = 0xbd // 189
	OP_UNKNOWN190          = 0xbe // 190
	OP_UNKNOWN191          = 0xbf // 191
	OP_UNKNOWN192          = 0xc0 // 192
	OP_UNKNOWN193          = 0xc1 // 193
	OP_UNKNOWN194          = 0xc2 // 194
	OP_UNKNOWN195          = 0xc3 // 195
	OP_UNKNOWN196          = 0xc4 // 196
	OP_UNKNOWN197          = 0xc5 // 197
	OP_UNKNOWN198          = 0xc6 // 198
	OP_UNKNOWN199          = 0xc7 // 199
	OP_UNKNOWN200          = 0xc8 // 200
	OP_UNKNOWN201          = 0xc9 // 201
	OP_UNKNOWN202          = 0xca // 202
	OP_UNKNOWN203          = 0xcb // 203
	OP_UNKNOWN204          = 0xcc // 204
	OP_UNKNOWN205          = 0xcd // 205
	OP_UNKNOWN206          = 0xce // 206
	OP_UNKNOWN207          = 0xcf // 207
	OP_UNKNOWN208          = 0xd0 // 208
	OP_UNKNOWN209          = 0xd1 // 209
	OP_UNKNOWN210          = 0xd2 // 210
	OP_UNKNOWN211          = 0xd3 // 211
	OP_UNKNOWN212          = 0xd4 // 212
	OP_UNKNOWN213          = 0xd5 // 213
	OP_UNKNOWN214          = 0xd6 // 214
	OP_UNKNOWN215          = 0xd7 // 215
	OP_UNKNOWN216          = 0xd8 // 216
	OP_UNKNOWN217          = 0xd9 // 217
	OP_UNKNOWN218          = 0xda // 218
	OP_UNKNOWN219          = 0xdb // 219
	OP_UNKNOWN220          = 0xdc // 220
	OP_UNKNOWN221          = 0xdd // 221
	OP_UNKNOWN222          = 0xde // 222
	OP_UNKNOWN223          = 0xdf // 223
	OP_UNKNOWN224          = 0xe0 // 224
	OP_UNKNOWN225          = 0xe1 // 225
	OP_UNKNOWN226          = 0xe2 // 226
	OP_UNKNOWN227          = 0xe3 // 227
	OP_UNKNOWN228          = 0xe4 // 228
	OP_UNKNOWN229          = 0xe5 // 229
	OP_UNKNOWN230          = 0xe6 // 230
	OP_UNKNOWN231          = 0xe7 // 231
	OP_UNKNOWN232          = 0xe8 // 232
	OP_UNKNOWN233          = 0xe9 // 233
	OP_UNKNOWN234          = 0xea // 234
	OP_UNKNOWN235          = 0xeb // 235
	OP_UNKNOWN236          = 0xec // 236
	OP_UNKNOWN237          = 0xed // 237
	OP_UNKNOWN238          = 0xee // 238
	OP_UNKNOWN239          = 0xef // 239
	OP_UNKNOWN240          = 0xf0 // 240
	OP_UNKNOWN241          = 0xf1 // 241
	OP_UNKNOWN242          = 0xf2 // 242
	OP_UNKNOWN243          = 0xf3 // 243
	OP_UNKNOWN244          = 0xf4 // 244
	OP_UNKNOWN245          = 0xf5 // 245
	OP_UNKNOWN246          = 0xf6 // 246
	OP_UNKNOWN247          = 0xf7 // 247
	OP_UNKNOWN248          = 0xf8 // 248
	OP_UNKNOWN249          = 0xf9 // 249
	OP_SMALLINTEGER        = 0xfa // 250 - bitcoin core internal
	OP_PUBKEYS             = 0xfb // 251 - bitcoin core internal
	OP_UNKNOWN252          = 0xfc // 252
	OP_PUBKEYHASH          = 0xfd // 253 - bitcoin core internal
	OP_PUBKEY              = 0xfe // 254 - bitcoin core internal
	OP_INVALIDOPCODE       = 0xff // 255 - bitcoin core internal
)

// Conditional execution constants.
const (
	OpCondFalse = 0
	OpCondTrue  = 1
	OpCondSkip  = 2
)

// opcodeArray holds details about all possible opcodes such as how many bytes
// the opcode and any associated data should take, its human-readable name, and
// the handler function.
var opcodeArray = [256]opcode{
	// Data push opcodes.
	OP_FALSE:     {OP_FALSE, "OP_0", 1},
	OP_DATA_1:    {OP_DATA_1, "OP_DATA_1", 2},
	OP_DATA_2:    {OP_DATA_2, "OP_DATA_2", 3},
	OP_DATA_3:    {OP_DATA_3, "OP_DATA_3", 4},
	OP_DATA_4:    {OP_DATA_4, "OP_DATA_4", 5},
	OP_DATA_5:    {OP_DATA_5, "OP_DATA_5", 6},
	OP_DATA_6:    {OP_DATA_6, "OP_DATA_6", 7},
	OP_DATA_7:    {OP_DATA_7, "OP_DATA_7", 8},
	OP_DATA_8:    {OP_DATA_8, "OP_DATA_8", 9},
	OP_DATA_9:    {OP_DATA_9, "OP_DATA_9", 10},
	OP_DATA_10:   {OP_DATA_10, "OP_DATA_10", 11},
	OP_DATA_11:   {OP_DATA_11, "OP_DATA_11", 12},
	OP_DATA_12:   {OP_DATA_12, "OP_DATA_12", 13},
	OP_DATA_13:   {OP_DATA_13, "OP_DATA_13", 14},
	OP_DATA_14:   {OP_DATA_14, "OP_DATA_14", 15},
	OP_DATA_15:   {OP_DATA_15, "OP_DATA_15", 16},
	OP_DATA_16:   {OP_DATA_16, "OP_DATA_16", 17},
	OP_DATA_17:   {OP_DATA_17, "OP_DATA_17", 18},
	OP_DATA_18:   {OP_DATA_18, "OP_DATA_18", 19},
	OP_DATA_19:   {OP_DATA_19, "OP_DATA_19", 20},
	OP_DATA_20:   {OP_DATA_20, "OP_DATA_20", 21},
	OP_DATA_21:   {OP_DATA_21, "OP_DATA_21", 22},
	OP_DATA_22:   {OP_DATA_22, "OP_DATA_22", 23},
	OP_DATA_23:   {OP_DATA_23, "OP_DATA_23", 24},
	OP_DATA_24:   {OP_DATA_24, "OP_DATA_24", 25},
	OP_DATA_25:   {OP_DATA_25, "OP_DATA_25", 26},
	OP_DATA_26:   {OP_DATA_26, "OP_DATA_26", 27},
	OP_DATA_27:   {OP_DATA_27, "OP_DATA_27", 28},
	OP_DATA_28:   {OP_DATA_28, "OP_DATA_28", 29},
	OP_DATA_29:   {OP_DATA_29, "OP_DATA_29", 30},
	OP_DATA_30:   {OP_DATA_30, "OP_DATA_30", 31},
	OP_DATA_31:   {OP_DATA_31, "OP_DATA_31", 32},
	OP_DATA_32:   {OP_DATA_32, "OP_DATA_32", 33},
	OP_DATA_33:   {OP_DATA_33, "OP_DATA_33", 34},
	OP_DATA_34:   {OP_DATA_34, "OP_DATA_34", 35},
	OP_DATA_35:   {OP_DATA_35, "OP_DATA_35", 36},
	OP_DATA_36:   {OP_DATA_36, "OP_DATA_36", 37},
	OP_DATA_37:   {OP_DATA_37, "OP_DATA_37", 38},
	OP_DATA_38:   {OP_DATA_38, "OP_DATA_38", 39},
	OP_DATA_39:   {OP_DATA_39, "OP_DATA_39", 40},
	OP_DATA_40:   {OP_DATA_40, "OP_DATA_40", 41},
	OP_DATA_41:   {OP_DATA_41, "OP_DATA_41", 42},
	OP_DATA_42:   {OP_DATA_42, "OP_DATA_42", 43},
	OP_DATA_43:   {OP_DATA_43, "OP_DATA_43", 44},
	OP_DATA_44:   {OP_DATA_44, "OP_DATA_44", 45},
	OP_DATA_45:   {OP_DATA_45, "OP_DATA_45", 46},
	OP_DATA_46:   {OP_DATA_46, "OP_DATA_46", 47},
	OP_DATA_47:   {OP_DATA_47, "OP_DATA_47", 48},
	OP_DATA_48:   {OP_DATA_48, "OP_DATA_48", 49},
	OP_DATA_49:   {OP_DATA_49, "OP_DATA_49", 50},
	OP_DATA_50:   {OP_DATA_50, "OP_DATA_50", 51},
	OP_DATA_51:   {OP_DATA_51, "OP_DATA_51", 52},
	OP_DATA_52:   {OP_DATA_52, "OP_DATA_52", 53},
	OP_DATA_53:   {OP_DATA_53, "OP_DATA_53", 54},
	OP_DATA_54:   {OP_DATA_54, "OP_DATA_54", 55},
	OP_DATA_55:   {OP_DATA_55, "OP_DATA_55", 56},
	OP_DATA_56:   {OP_DATA_56, "OP_DATA_56", 57},
	OP_DATA_57:   {OP_DATA_57, "OP_DATA_57", 58},
	OP_DATA_58:   {OP_DATA_58, "OP_DATA_58", 59},
	OP_DATA_59:   {OP_DATA_59, "OP_DATA_59", 60},
	OP_DATA_60:   {OP_DATA_60, "OP_DATA_60", 61},
	OP_DATA_61:   {OP_DATA_61, "OP_DATA_61", 62},
	OP_DATA_62:   {OP_DATA_62, "OP_DATA_62", 63},
	OP_DATA_63:   {OP_DATA_63, "OP_DATA_63", 64},
	OP_DATA_64:   {OP_DATA_64, "OP_DATA_64", 65},
	OP_DATA_65:   {OP_DATA_65, "OP_DATA_65", 66},
	OP_DATA_66:   {OP_DATA_66, "OP_DATA_66", 67},
	OP_DATA_67:   {OP_DATA_67, "OP_DATA_67", 68},
	OP_DATA_68:   {OP_DATA_68, "OP_DATA_68", 69},
	OP_DATA_69:   {OP_DATA_69, "OP_DATA_69", 70},
	OP_DATA_70:   {OP_DATA_70, "OP_DATA_70", 71},
	OP_DATA_71:   {OP_DATA_71, "OP_DATA_71", 72},
	OP_DATA_72:   {OP_DATA_72, "OP_DATA_72", 73},
	OP_DATA_73:   {OP_DATA_73, "OP_DATA_73", 74},
	OP_DATA_74:   {OP_DATA_74, "OP_DATA_74", 75},
	OP_DATA_75:   {OP_DATA_75, "OP_DATA_75", 76},
	OP_PUSHDATA1: {OP_PUSHDATA1, "OP_PUSHDATA1", -1},
	OP_PUSHDATA2: {OP_PUSHDATA2, "OP_PUSHDATA2", -2},
	OP_PUSHDATA4: {OP_PUSHDATA4, "OP_PUSHDATA4", -4},
	OP_1NEGATE:   {OP_1NEGATE, "OP_1NEGATE", 1},
	OP_RESERVED:  {OP_RESERVED, "OP_RESERVED", 1},
	OP_TRUE:      {OP_TRUE, "OP_1", 1},
	OP_2:         {OP_2, "OP_2", 1},
	OP_3:         {OP_3, "OP_3", 1},
	OP_4:         {OP_4, "OP_4", 1},
	OP_5:         {OP_5, "OP_5", 1},
	OP_6:         {OP_6, "OP_6", 1},
	OP_7:         {OP_7, "OP_7", 1},
	OP_8:         {OP_8, "OP_8", 1},
	OP_9:         {OP_9, "OP_9", 1},
	OP_10:        {OP_10, "OP_10", 1},
	OP_11:        {OP_11, "OP_11", 1},
	OP_12:        {OP_12, "OP_12", 1},
	OP_13:        {OP_13, "OP_13", 1},
	OP_14:        {OP_14, "OP_14", 1},
	OP_15:        {OP_15, "OP_15", 1},
	OP_16:        {OP_16, "OP_16", 1},

	// Control opcodes.
	OP_NOP:                 {OP_NOP, "OP_NOP", 1},
	OP_VER:                 {OP_VER, "OP_VER", 1},
	OP_IF:                  {OP_IF, "OP_IF", 1},
	OP_NOTIF:               {OP_NOTIF, "OP_NOTIF", 1},
	OP_VERIF:               {OP_VERIF, "OP_VERIF", 1},
	OP_VERNOTIF:            {OP_VERNOTIF, "OP_VERNOTIF", 1},
	OP_ELSE:                {OP_ELSE, "OP_ELSE", 1},
	OP_ENDIF:               {OP_ENDIF, "OP_ENDIF", 1},
	OP_VERIFY:              {OP_VERIFY, "OP_VERIFY", 1},
	OP_RETURN:              {OP_RETURN, "OP_RETURN", 1},
	OP_CHECKLOCKTIMEVERIFY: {OP_CHECKLOCKTIMEVERIFY, "OP_CHECKLOCKTIMEVERIFY", 1},
	OP_CHECKSEQUENCEVERIFY: {OP_CHECKSEQUENCEVERIFY, "OP_CHECKSEQUENCEVERIFY", 1},

	// Stack opcodes.
	OP_TOALTSTACK:   {OP_TOALTSTACK, "OP_TOALTSTACK", 1},
	OP_FROMALTSTACK: {OP_FROMALTSTACK, "OP_FROMALTSTACK", 1},
	OP_2DROP:        {OP_2DROP, "OP_2DROP", 1},
	OP_2DUP:         {OP_2DUP, "OP_2DUP", 1},
	OP_3DUP:         {OP_3DUP, "OP_3DUP", 1},
	OP_2OVER:        {OP_2OVER, "OP_2OVER", 1},
	OP_2ROT:         {OP_2ROT, "OP_2ROT", 1},
	OP_2SWAP:        {OP_2SWAP, "OP_2SWAP", 1},
	OP_IFDUP:        {OP_IFDUP, "OP_IFDUP", 1},
	OP_DEPTH:        {OP_DEPTH, "OP_DEPTH", 1},
	OP_DROP:         {OP_DROP, "OP_DROP", 1},
	OP_DUP:          {OP_DUP, "OP_DUP", 1},
	OP_NIP:          {OP_NIP, "OP_NIP", 1},
	OP_OVER:         {OP_OVER, "OP_OVER", 1},
	OP_PICK:         {OP_PICK, "OP_PICK", 1},
	OP_ROLL:         {OP_ROLL, "OP_ROLL", 1},
	OP_ROT:          {OP_ROT, "OP_ROT", 1},
	OP_SWAP:         {OP_SWAP, "OP_SWAP", 1},
	OP_TUCK:         {OP_TUCK, "OP_TUCK", 1},

	// Splice opcodes.
	OP_CAT:    {OP_CAT, "OP_CAT", 1},
	OP_SUBSTR: {OP_SUBSTR, "OP_SUBSTR", 1},
	OP_LEFT:   {OP_LEFT, "OP_LEFT", 1},
	OP_RIGHT:  {OP_RIGHT, "OP_RIGHT", 1},
	OP_SIZE:   {OP_SIZE, "OP_SIZE", 1},

	// Bitwise logic opcodes.
	OP_INVERT:      {OP_INVERT, "OP_INVERT", 1},
	OP_AND:         {OP_AND, "OP_AND", 1},
	OP_OR:          {OP_OR, "OP_OR", 1},
	OP_XOR:         {OP_XOR, "OP_XOR", 1},
	OP_EQUAL:       {OP_EQUAL, "OP_EQUAL", 1},
	OP_EQUALVERIFY: {OP_EQUALVERIFY, "OP_EQUALVERIFY", 1},
	OP_RESERVED1:   {OP_RESERVED1, "OP_RESERVED1", 1},
	OP_RESERVED2:   {OP_RESERVED2, "OP_RESERVED2", 1},

	// Numeric related opcodes.
	OP_1ADD:               {OP_1ADD, "OP_1ADD", 1},
	OP_1SUB:               {OP_1SUB, "OP_1SUB", 1},
	OP_2MUL:               {OP_2MUL, "OP_2MUL", 1},
	OP_2DIV:               {OP_2DIV, "OP_2DIV", 1},
	OP_NEGATE:             {OP_NEGATE, "OP_NEGATE", 1},
	OP_ABS:                {OP_ABS, "OP_ABS", 1},
	OP_NOT:                {OP_NOT, "OP_NOT", 1},
	OP_0NOTEQUAL:          {OP_0NOTEQUAL, "OP_0NOTEQUAL", 1},
	OP_ADD:                {OP_ADD, "OP_ADD", 1},
	OP_SUB:                {OP_SUB, "OP_SUB", 1},
	OP_MUL:                {OP_MUL, "OP_MUL", 1},
	OP_DIV:                {OP_DIV, "OP_DIV", 1},
	OP_MOD:                {OP_MOD, "OP_MOD", 1},
	OP_LSHIFT:             {OP_LSHIFT, "OP_LSHIFT", 1},
	OP_RSHIFT:             {OP_RSHIFT, "OP_RSHIFT", 1},
	OP_BOOLAND:            {OP_BOOLAND, "OP_BOOLAND", 1},
	OP_BOOLOR:             {OP_BOOLOR, "OP_BOOLOR", 1},
	OP_NUMEQUAL:           {OP_NUMEQUAL, "OP_NUMEQUAL", 1},
	OP_NUMEQUALVERIFY:     {OP_NUMEQUALVERIFY, "OP_NUMEQUALVERIFY", 1},
	OP_NUMNOTEQUAL:        {OP_NUMNOTEQUAL, "OP_NUMNOTEQUAL", 1},
	OP_LESSTHAN:           {OP_LESSTHAN, "OP_LESSTHAN", 1},
	OP_GREATERTHAN:        {OP_GREATERTHAN, "OP_GREATERTHAN", 1},
	OP_LESSTHANOREQUAL:    {OP_LESSTHANOREQUAL, "OP_LESSTHANOREQUAL", 1},
	OP_GREATERTHANOREQUAL: {OP_GREATERTHANOREQUAL, "OP_GREATERTHANOREQUAL", 1},
	OP_MIN:                {OP_MIN, "OP_MIN", 1},
	OP_MAX:                {OP_MAX, "OP_MAX", 1},
	OP_WITHIN:             {OP_WITHIN, "OP_WITHIN", 1},

	// Crypto opcodes.
	OP_RIPEMD160:           {OP_RIPEMD160, "OP_RIPEMD160", 1},
	OP_SHA1:                {OP_SHA1, "OP_SHA1", 1},
	OP_SHA256:              {OP_SHA256, "OP_SHA256", 1},
	OP_HASH160:             {OP_HASH160, "OP_HASH160", 1},
	OP_HASH256:             {OP_HASH256, "OP_HASH256", 1},
	OP_CODESEPARATOR:       {OP_CODESEPARATOR, "OP_CODESEPARATOR", 1},
	OP_CHECKSIG:            {OP_CHECKSIG, "OP_CHECKSIG", 1},
	OP_CHECKSIGVERIFY:      {OP_CHECKSIGVERIFY, "OP_CHECKSIGVERIFY", 1},
	OP_CHECKMULTISIG:       {OP_CHECKMULTISIG, "OP_CHECKMULTISIG", 1},
	OP_CHECKMULTISIGVERIFY: {OP_CHECKMULTISIGVERIFY, "OP_CHECKMULTISIGVERIFY", 1},

	// Reserved opcodes.
	OP_NOP1:  {OP_NOP1, "OP_NOP1", 1},
	OP_NOP4:  {OP_NOP4, "OP_NOP4", 1},
	OP_NOP5:  {OP_NOP5, "OP_NOP5", 1},
	OP_NOP6:  {OP_NOP6, "OP_NOP6", 1},
	OP_NOP7:  {OP_NOP7, "OP_NOP7", 1},
	OP_NOP8:  {OP_NOP8, "OP_NOP8", 1},
	OP_NOP9:  {OP_NOP9, "OP_NOP9", 1},
	OP_NOP10: {OP_NOP10, "OP_NOP10", 1},

	// Undefined opcodes.
	OP_UNKNOWN186: {OP_UNKNOWN186, "OP_UNKNOWN186", 1},
	OP_UNKNOWN187: {OP_UNKNOWN187, "OP_UNKNOWN187", 1},
	OP_UNKNOWN188: {OP_UNKNOWN188, "OP_UNKNOWN188", 1},
	OP_UNKNOWN189: {OP_UNKNOWN189, "OP_UNKNOWN189", 1},
	OP_UNKNOWN190: {OP_UNKNOWN190, "OP_UNKNOWN190", 1},
	OP_UNKNOWN191: {OP_UNKNOWN191, "OP_UNKNOWN191", 1},
	OP_UNKNOWN192: {OP_UNKNOWN192, "OP_UNKNOWN192", 1},
	OP_UNKNOWN193: {OP_UNKNOWN193, "OP_UNKNOWN193", 1},
	OP_UNKNOWN194: {OP_UNKNOWN194, "OP_UNKNOWN194", 1},
	OP_UNKNOWN195: {OP_UNKNOWN195, "OP_UNKNOWN195", 1},
	OP_UNKNOWN196: {OP_UNKNOWN196, "OP_UNKNOWN196", 1},
	OP_UNKNOWN197: {OP_UNKNOWN197, "OP_UNKNOWN197", 1},
	OP_UNKNOWN198: {OP_UNKNOWN198, "OP_UNKNOWN198", 1},
	OP_UNKNOWN199: {OP_UNKNOWN199, "OP_UNKNOWN199", 1},
	OP_UNKNOWN200: {OP_UNKNOWN200, "OP_UNKNOWN200", 1},
	OP_UNKNOWN201: {OP_UNKNOWN201, "OP_UNKNOWN201", 1},
	OP_UNKNOWN202: {OP_UNKNOWN202, "OP_UNKNOWN202", 1},
	OP_UNKNOWN203: {OP_UNKNOWN203, "OP_UNKNOWN203", 1},
	OP_UNKNOWN204: {OP_UNKNOWN204, "OP_UNKNOWN204", 1},
	OP_UNKNOWN205: {OP_UNKNOWN205, "OP_UNKNOWN205", 1},
	OP_UNKNOWN206: {OP_UNKNOWN206, "OP_UNKNOWN206", 1},
	OP_UNKNOWN207: {OP_UNKNOWN207, "OP_UNKNOWN207", 1},
	OP_UNKNOWN208: {OP_UNKNOWN208, "OP_UNKNOWN208", 1},
	OP_UNKNOWN209: {OP_UNKNOWN209, "OP_UNKNOWN209", 1},
	OP_UNKNOWN210: {OP_UNKNOWN210, "OP_UNKNOWN210", 1},
	OP_UNKNOWN211: {OP_UNKNOWN211, "OP_UNKNOWN211", 1},
	OP_UNKNOWN212: {OP_UNKNOWN212, "OP_UNKNOWN212", 1},
	OP_UNKNOWN213: {OP_UNKNOWN213, "OP_UNKNOWN213", 1},
	OP_UNKNOWN214: {OP_UNKNOWN214, "OP_UNKNOWN214", 1},
	OP_UNKNOWN215: {OP_UNKNOWN215, "OP_UNKNOWN215", 1},
	OP_UNKNOWN216: {OP_UNKNOWN216, "OP_UNKNOWN216", 1},
	OP_UNKNOWN217: {OP_UNKNOWN217, "OP_UNKNOWN217", 1},
	OP_UNKNOWN218: {OP_UNKNOWN218, "OP_UNKNOWN218", 1},
	OP_UNKNOWN219: {OP_UNKNOWN219, "OP_UNKNOWN219", 1},
	OP_UNKNOWN220: {OP_UNKNOWN220, "OP_UNKNOWN220", 1},
	OP_UNKNOWN221: {OP_UNKNOWN221, "OP_UNKNOWN221", 1},
	OP_UNKNOWN222: {OP_UNKNOWN222, "OP_UNKNOWN222", 1},
	OP_UNKNOWN223: {OP_UNKNOWN223, "OP_UNKNOWN223", 1},
	OP_UNKNOWN224: {OP_UNKNOWN224, "OP_UNKNOWN224", 1},
	OP_UNKNOWN225: {OP_UNKNOWN225, "OP_UNKNOWN225", 1},
	OP_UNKNOWN226: {OP_UNKNOWN226, "OP_UNKNOWN226", 1},
	OP_UNKNOWN227: {OP_UNKNOWN227, "OP_UNKNOWN227", 1},
	OP_UNKNOWN228: {OP_UNKNOWN228, "OP_UNKNOWN228", 1},
	OP_UNKNOWN229: {OP_UNKNOWN229, "OP_UNKNOWN229", 1},
	OP_UNKNOWN230: {OP_UNKNOWN230, "OP_UNKNOWN230", 1},
	OP_UNKNOWN231: {OP_UNKNOWN231, "OP_UNKNOWN231", 1},
	OP_UNKNOWN232: {OP_UNKNOWN232, "OP_UNKNOWN232", 1},
	OP_UNKNOWN233: {OP_UNKNOWN233, "OP_UNKNOWN233", 1},
	OP_UNKNOWN234: {OP_UNKNOWN234, "OP_UNKNOWN234", 1},
	OP_UNKNOWN235: {OP_UNKNOWN235, "OP_UNKNOWN235", 1},
	OP_UNKNOWN236: {OP_UNKNOWN236, "OP_UNKNOWN236", 1},
	OP_UNKNOWN237: {OP_UNKNOWN237, "OP_UNKNOWN237", 1},
	OP_UNKNOWN238: {OP_UNKNOWN238, "OP_UNKNOWN238", 1},
	OP_UNKNOWN239: {OP_UNKNOWN239, "OP_UNKNOWN239", 1},
	OP_UNKNOWN240: {OP_UNKNOWN240, "OP_UNKNOWN240", 1},
	OP_UNKNOWN241: {OP_UNKNOWN241, "OP_UNKNOWN241", 1},
	OP_UNKNOWN242: {OP_UNKNOWN242, "OP_UNKNOWN242", 1},
	OP_UNKNOWN243: {OP_UNKNOWN243, "OP_UNKNOWN243", 1},
	OP_UNKNOWN244: {OP_UNKNOWN244, "OP_UNKNOWN244", 1},
	OP_UNKNOWN245: {OP_UNKNOWN245, "OP_UNKNOWN245", 1},
	OP_UNKNOWN246: {OP_UNKNOWN246, "OP_UNKNOWN246", 1},
	OP_UNKNOWN247: {OP_UNKNOWN247, "OP_UNKNOWN247", 1},
	OP_UNKNOWN248: {OP_UNKNOWN248, "OP_UNKNOWN248", 1},
	OP_UNKNOWN249: {OP_UNKNOWN249, "OP_UNKNOWN249", 1},

	// Bitcoin Core internal use opcode.  Defined here for completeness.
	OP_SMALLINTEGER: {OP_SMALLINTEGER, "OP_SMALLINTEGER", 1},
	OP_PUBKEYS:      {OP_PUBKEYS, "OP_PUBKEYS", 1},
	OP_UNKNOWN252:   {OP_UNKNOWN252, "OP_UNKNOWN252", 1},
	OP_PUBKEYHASH:   {OP_PUBKEYHASH, "OP_PUBKEYHASH", 1},
	OP_PUBKEY:       {OP_PUBKEY, "OP_PUBKEY", 1},

	OP_INVALIDOPCODE: {OP_INVALIDOPCODE, "OP_INVALIDOPCODE", 1},
}

// bytes returns any data associated with the opcode encoded as it would be in
// a script.  This is used for unparsing scripts from parsed opcodes.
func (pop *parsedOpcode) bytes() ([]byte, error) {
	var retbytes []byte
	if pop.opcode.length > 0 {
		retbytes = make([]byte, 1, pop.opcode.length)
	} else {
		retbytes = make([]byte, 1, 1+len(pop.data)-
			pop.opcode.length)
	}

	retbytes[0] = pop.opcode.value
	if pop.opcode.length == 1 {
		if len(pop.data) != 0 {
			str := fmt.Sprintf("internal consistency error - "+
				"parsed opcode %s has data length %d when %d "+
				"was expected", pop.opcode.name, len(pop.data),
				0)
			return nil, errors.New(str)
		}
		return retbytes, nil
	}
	nbytes := pop.opcode.length
	if pop.opcode.length < 0 {
		l := len(pop.data)
		// tempting just to hardcode to avoid the complexity here.
		switch pop.opcode.length {
		case -1:
			retbytes = append(retbytes, byte(l))
			nbytes = int(retbytes[1]) + len(retbytes)
		case -2:
			retbytes = append(retbytes, byte(l&0xff),
				byte(l>>8&0xff))
			nbytes = int(binary.LittleEndian.Uint16(retbytes[1:])) +
				len(retbytes)
		case -4:
			retbytes = append(retbytes, byte(l&0xff),
				byte((l>>8)&0xff), byte((l>>16)&0xff),
				byte((l>>24)&0xff))
			nbytes = int(binary.LittleEndian.Uint32(retbytes[1:])) +
				len(retbytes)
		}
	}

	retbytes = append(retbytes, pop.data...)

	if len(retbytes) != nbytes {
		str := fmt.Sprintf("internal consistency error - "+
			"parsed opcode %s has data length %d when %d was "+
			"expected", pop.opcode.name, len(retbytes), nbytes)
		return nil, errors.New(str)
	}

	return retbytes, nil
}

// unparseScript reversed the action of parseScript and returns the
// parsedOpcodes as a list of bytes
func unparseScript(pops []parsedOpcode) ([]byte, error) {
	script := make([]byte, 0, len(pops))
	for _, pop := range pops {
		b, err := pop.bytes()
		if err != nil {
			return nil, err
		}
		script = append(script, b...)
	}
	return script, nil
}

// isWitnessPubKeyHash returns true if the passed script is a
// pay-to-witness-pubkey-hash, and false otherwise.
func isWitnessPubKeyHash(pops []parsedOpcode) bool {
	return len(pops) == 2 &&
		pops[0].opcode.value == OP_0 &&
		pops[1].opcode.value == OP_DATA_20
}

// WriteVarBytes serializes a variable length byte array to w as a varInt
// containing the number of bytes, followed by the bytes themselves.
func WriteVarBytes(w io.Writer, pver uint32, bytes []byte) error {
	slen := uint64(len(bytes))
	err := WriteVarInt(w, pver, slen)
	if err != nil {
		return err
	}

	_, err = w.Write(bytes)
	return err
}

// WriteTxOut encodes to into the bitcoin protocol encoding for a transaction
// output (TxOut) to w.
//
// NOTE: This function is exported in order to allow txscript to compute the
// new sighashes for witness transactions (BIP0143).
func WriteTxOut(w io.Writer, pver uint32, version int32, to *TxOut) error {
	err := binarySerializer.PutUint64(w, littleEndian, uint64(to.Value))
	if err != nil {
		return err
	}

	return WriteVarBytes(w, pver, to.PkScript)
}

// calcWitnessSignatureHash computes the sighash digest of a transaction's
// segwit input using the new, optimized digest calculation algorithm defined
// in BIP0143: https://github.com/bitcoin/bips/blob/master/bip-0143.mediawiki.
// This function makes use of pre-calculated sighash fragments stored within
// the passed HashCache to eliminate duplicate hashing computations when
// calculating the final digest, reducing the complexity from O(N^2) to O(N).
// Additionally, signatures now cover the input value of the referenced unspent
// output. This allows offline, or hardware wallets to compute the exact amount
// being spent, in addition to the final transaction fee. In the case the
// wallet if fed an invalid input amount, the real sighash will differ causing
// the produced signature to be invalid.
func calcWitnessSignatureHash(subScript []parsedOpcode, sigHashes *TxSigHashes,
	hashType SigHashType, tx *MsgTx, idx int, amt int64) ([]byte, error) {

	// As a sanity check, ensure the passed input index for the transaction
	// is valid.
	if idx > len(tx.TxIn)-1 {
		return nil, fmt.Errorf("idx %d but %d txins", idx, len(tx.TxIn))
	}

	// We'll utilize this buffer throughout to incrementally calculate
	// the signature hash for this transaction.
	var sigHash bytes.Buffer

	// First write out, then encode the transaction's version number.
	var bVersion [4]byte
	binary.LittleEndian.PutUint32(bVersion[:], uint32(tx.Version))
	sigHash.Write(bVersion[:])

	// Next write out the possibly pre-calculated hashes for the sequence
	// numbers of all inputs, and the hashes of the previous outs for all
	// outputs.
	var zeroHash chainhash.Hash

	// If anyone can pay isn't active, then we can use the cached
	// hashPrevOuts, otherwise we just write zeroes for the prev outs.
	if hashType&SigHashAnyOneCanPay == 0 {
		sigHash.Write(sigHashes.HashPrevOuts[:])
	} else {
		sigHash.Write(zeroHash[:])
	}

	// If the sighash isn't anyone can pay, single, or none, the use the
	// cached hash sequences, otherwise write all zeroes for the
	// hashSequence.
	if hashType&SigHashAnyOneCanPay == 0 &&
		hashType&sigHashMask != SigHashSingle &&
		hashType&sigHashMask != SigHashNone {
		sigHash.Write(sigHashes.HashSequence[:])
	} else {
		sigHash.Write(zeroHash[:])
	}

	txIn := tx.TxIn[idx]

	// Next, write the outpoint being spent.
	sigHash.Write(txIn.PreviousOutPoint.Hash[:])
	var bIndex [4]byte
	binary.LittleEndian.PutUint32(bIndex[:], txIn.PreviousOutPoint.Index)
	sigHash.Write(bIndex[:])

	if isWitnessPubKeyHash(subScript) {
		// The script code for a p2wkh is a length prefix varint for
		// the next 25 bytes, followed by a re-creation of the original
		// p2pkh pk script.
		sigHash.Write([]byte{0x19})
		sigHash.Write([]byte{OP_DUP})
		sigHash.Write([]byte{OP_HASH160})
		sigHash.Write([]byte{OP_DATA_20})
		sigHash.Write(subScript[1].data)
		sigHash.Write([]byte{OP_EQUALVERIFY})
		sigHash.Write([]byte{OP_CHECKSIG})
	} else {
		// For p2wsh outputs, and future outputs, the script code is
		// the original script, with all code separators removed,
		// serialized with a var int length prefix.
		rawScript, _ := unparseScript(subScript)
		WriteVarBytes(&sigHash, 0, rawScript)
	}

	// Next, add the input amount, and sequence number of the input being
	// signed.
	var bAmount [8]byte
	binary.LittleEndian.PutUint64(bAmount[:], uint64(amt))
	sigHash.Write(bAmount[:])
	var bSequence [4]byte
	binary.LittleEndian.PutUint32(bSequence[:], txIn.Sequence)
	sigHash.Write(bSequence[:])

	// If the current signature mode isn't single, or none, then we can
	// re-use the pre-generated hashoutputs sighash fragment. Otherwise,
	// we'll serialize and add only the target output index to the signature
	// pre-image.
	if hashType&SigHashSingle != SigHashSingle &&
		hashType&SigHashNone != SigHashNone {
		sigHash.Write(sigHashes.HashOutputs[:])
	} else if hashType&sigHashMask == SigHashSingle && idx < len(tx.TxOut) {
		var b bytes.Buffer
		WriteTxOut(&b, 0, 0, tx.TxOut[idx])
		sigHash.Write(chainhash.DoubleHashB(b.Bytes()))
	} else {
		sigHash.Write(zeroHash[:])
	}

	// Finally, write out the transaction's locktime, and the sig hash
	// type.
	var bLockTime [4]byte
	binary.LittleEndian.PutUint32(bLockTime[:], tx.LockTime)
	sigHash.Write(bLockTime[:])
	var bHashType [4]byte
	binary.LittleEndian.PutUint32(bHashType[:], uint32(hashType))
	sigHash.Write(bHashType[:])

	return chainhash.DoubleHashB(sigHash.Bytes()), nil
}

// TxWitness defines the witness for a TxIn. A witness is to be interpreted as
// a slice of byte slices, or a stack with one or many elements.
type TxWitness [][]byte

type TxIn struct {
	PreviousOutPoint OutPoint
	SignatureScript  []byte
	Witness          TxWitness
	Sequence         uint32
}

type TxOut struct {
	Value    int64
	PkScript []byte
}

type MsgTx struct {
	Version  int32
	TxIn     []*TxIn
	TxOut    []*TxOut
	LockTime uint32
}

// parseScript preparses the script in bytes into a list of parsedOpcodes while
// applying a number of sanity checks.
func parseScript(script []byte) ([]parsedOpcode, error) {
	return parseScriptTemplate(script, &opcodeArray)
}

// parseScriptTemplate is the same as parseScript but allows the passing of the
// template list for testing purposes.  When there are parse errors, it returns
// the list of parsed opcodes up to the point of failure along with the error.
func parseScriptTemplate(script []byte, opcodes *[256]opcode) ([]parsedOpcode, error) {
	retScript := make([]parsedOpcode, 0, len(script))
	for i := 0; i < len(script); {
		instr := script[i]
		op := &opcodes[instr]
		pop := parsedOpcode{opcode: op}

		// Parse data out of instruction.
		switch {
		// No additional data.  Note that some of the opcodes, notably
		// OP_1NEGATE, OP_0, and OP_[1-16] represent the data
		// themselves.
		case op.length == 1:
			i++

		// Data pushes of specific lengths -- OP_DATA_[1-75].
		case op.length > 1:
			if len(script[i:]) < op.length {
				str := fmt.Sprintf("opcode %s requires %d "+
					"bytes, but script only has %d remaining",
					op.name, op.length, len(script[i:]))
				return retScript, errors.New(str)
			}

			// Slice out the data.
			pop.data = script[i+1 : i+op.length]
			i += op.length

		// Data pushes with parsed lengths -- OP_PUSHDATAP{1,2,4}.
		case op.length < 0:
			var l uint
			off := i + 1

			if len(script[off:]) < -op.length {
				str := fmt.Sprintf("opcode %s requires %d "+
					"bytes, but script only has %d remaining",
					op.name, -op.length, len(script[off:]))
				return retScript, errors.New(str)
			}

			// Next -length bytes are little endian length of data.
			switch op.length {
			case -1:
				l = uint(script[off])
			case -2:
				l = ((uint(script[off+1]) << 8) |
					uint(script[off]))
			case -4:
				l = ((uint(script[off+3]) << 24) |
					(uint(script[off+2]) << 16) |
					(uint(script[off+1]) << 8) |
					uint(script[off]))
			default:
				str := fmt.Sprintf("invalid opcode length %d",
					op.length)
				return retScript, errors.New(str)
			}

			// Move offset to beginning of the data.
			off += -op.length

			// Disallow entries that do not fit script or were
			// sign extended.
			if int(l) > len(script[off:]) || int(l) < 0 {
				str := fmt.Sprintf("opcode %s pushes %d bytes, "+
					"but script only has %d remaining",
					op.name, int(l), len(script[off:]))
				return retScript, errors.New(str)
			}

			pop.data = script[off : off+int(l)]
			i += 1 - op.length + int(l)
		}

		retScript = append(retScript, pop)
	}

	return retScript, nil
}

// RawTxInWitnessSignature returns the serialized ECDA signature for the input
// idx of the given transaction, with the hashType appended to it. This
// function is identical to RawTxInSignature, however the signature generated
// signs a new sighash digest defined in BIP0143.
func RawTxInWitnessSignature(tx *MsgTx, sigHashes *TxSigHashes, idx int,
	amt int64, subScript []byte, hashType SigHashType,
	key *btcec.PrivateKey) ([]byte, error) {

	parsedScript, err := parseScript(subScript)
	if err != nil {
		return nil, fmt.Errorf("cannot parse output script: %v", err)
	}

	hash, err := calcWitnessSignatureHash(parsedScript, sigHashes, hashType, tx,
		idx, amt)
	if err != nil {
		return nil, err
	}

	signature, err := key.Sign(hash)
	if err != nil {
		return nil, fmt.Errorf("cannot sign tx input: %s", err)
	}

	return append(signature.Serialize(), byte(hashType)), nil
}

// WitnessSignature creates an input witness stack for tx to spend BTC sent
// from a previous output to the owner of privKey using the p2wkh script
// template. The passed transaction must contain all the inputs and outputs as
// dictated by the passed hashType. The signature generated observes the new
// transaction digest algorithm defined within BIP0143.
func WitnessSignature(tx *MsgTx, sigHashes *TxSigHashes, idx int, amt int64,
	subscript []byte, hashType SigHashType, privKey *btcec.PrivateKey,
	compress bool) (TxWitness, error) {

	sig, err := RawTxInWitnessSignature(tx, sigHashes, idx, amt, subscript,
		hashType, privKey)
	if err != nil {
		return nil, err
	}

	pk := (*btcec.PublicKey)(&privKey.PublicKey)
	var pkData []byte
	if compress {
		pkData = pk.SerializeCompressed()
	} else {
		pkData = pk.SerializeUncompressed()
	}

	// A witness script is actually a stack, so we return an array of byte
	// slices here, rather than a single byte slice.
	return TxWitness{sig, pkData}, nil
}

// InputScript represents any script inputs required to redeem a previous
// output. This struct is used rather than just a witness, or scripSig in
// order to accommodate nested p2sh which utilizes both types of input scripts.
type InputScript struct {
	Witness   [][]byte
	ScriptSig []byte
}

// SignDescriptor houses the necessary information required to successfully sign
// a given output. This struct is used by the Signer interface in order to gain
// access to critical data needed to generate a valid signature.
type SignDescriptor struct {
	// Pubkey is the public key to which the signature should be generated
	// over. The Signer should then generate a signature with the private
	// key corresponding to this public key.
	PubKey *btcec.PublicKey

	// SingleTweak is a scalar value that will be added to the private key
	// corresponding to the above public key to obtain the private key to
	// be used to sign this input. This value is typically derived via the
	// following computation:
	//
	//  * derivedKey = privkey + sha256(perCommitmentPoint || pubKey) mod N
	//
	// NOTE: If this value is nil, then the input can be signed using only
	// the above public key. Either a SingleTweak should be set or a
	// DoubleTweak, not both.
	SingleTweak []byte

	// DoubleTweak is a private key that will be used in combination with
	// its corresponding private key to derive the private key that is to
	// be used to sign the target input. Within the Lightning protocol,
	// this value is typically the commitment secret from a previously
	// revoked commitment transaction. This value is in combination with
	// two hash values, and the original private key to derive the private
	// key to be used when signing.
	//
	//  * k = (privKey*sha256(pubKey || tweakPub) +
	//        tweakPriv*sha256(tweakPub || pubKey)) mod N
	//
	// NOTE: If this value is nil, then the input can be signed using only
	// the above public key. Either a SingleTweak should be set or a
	// DoubleTweak, not both.
	DoubleTweak *btcec.PrivateKey

	// WitnessScript is the full script required to properly redeem the
	// output. This field will only be populated if a p2wsh or a p2sh
	// output is being signed.
	WitnessScript []byte

	// Output is the target output which should be signed. The PkScript and
	// Value fields within the output should be properly populated,
	// otherwise an invalid signature may be generated.
	Output *TxOut

	// HashType is the target sighash type that should be used when
	// generating the final sighash, and signature.
	HashType SigHashType

	// SigHashes is the pre-computed sighash midstate to be used when
	// generating the final sighash for signing.
	SigHashes *TxSigHashes

	// InputIndex is the target input within the transaction that should be
	// signed.
	InputIndex int
}

func privKeyForOutputAddrInScript(script []byte) (bool, *btcec.PrivateKey) {
  panic("not implemented")
  return false, nil
}

// maybeTweakPrivKey examines the single and double tweak parameters on the
// passed sign descriptor and may perform a mapping on the passed private key
// in order to utilize the tweaks, if populated.
func maybeTweakPrivKey(signDesc *SignDescriptor,
	privKey *btcec.PrivateKey) (*btcec.PrivateKey, error) {

	var retPriv *btcec.PrivateKey
	switch {

	case signDesc.SingleTweak != nil:
		retPriv = TweakPrivKey(privKey,
			signDesc.SingleTweak)

	case signDesc.DoubleTweak != nil:
		retPriv = DeriveRevocationPrivKey(privKey,
			signDesc.DoubleTweak)

	default:
		retPriv = privKey
	}

	return retPriv, nil
}

func ReadVarInt(r io.Reader, pver uint32) (uint64, error) {
    discriminant, err := binarySerializer.Uint8(r)
    if err != nil {
        return 0, err
    }

    var rv uint64
    switch discriminant {
    case 0xff:
        sv, err := binarySerializer.Uint64(r, littleEndian)
        if err != nil {
            return 0, err
        }
        rv = sv

        // The encoding is not canonical if the value could have been
        // encoded using fewer bytes.
        min := uint64(0x100000000)
        if rv < min {
            panic("ReadVarInt")
        }

    case 0xfe:
        sv, err := binarySerializer.Uint32(r, littleEndian)
        if err != nil {
            return 0, err
        }
        rv = uint64(sv)

        // The encoding is not canonical if the value could have been
        // encoded using fewer bytes.
        min := uint64(0x10000)
        if rv < min {
            panic("ReadVarInt")
        }

    case 0xfd:
        sv, err := binarySerializer.Uint16(r, littleEndian)
        if err != nil {
            return 0, err
        }
        rv = uint64(sv)

        // The encoding is not canonical if the value could have been
        // encoded using fewer bytes.
        min := uint64(0xfd)
        if rv < min {
            panic("ReadVarInt")
        }

    default:
        rv = uint64(discriminant)
    }

    return rv, nil
}

// Uint16 reads two bytes from the provided reader using a buffer from the
// free list, converts it to a number using the provided byte order, and returns
// the resulting uint16.
func (l binaryFreeList) Uint16(r io.Reader, byteOrder binary.ByteOrder) (uint16, error) {
	buf := l.Borrow()[:2]
	if _, err := io.ReadFull(r, buf); err != nil {
		l.Return(buf)
		return 0, err
	}
	rv := byteOrder.Uint16(buf)
	l.Return(buf)
	return rv, nil
}

// Uint8 reads a single byte from the provided reader using a buffer from the
// free list and returns it as a uint8.
func (l binaryFreeList) Uint8(r io.Reader) (uint8, error) {
	buf := l.Borrow()[:1]
	if _, err := io.ReadFull(r, buf); err != nil {
		l.Return(buf)
		return 0, err
	}
	rv := buf[0]
	l.Return(buf)
	return rv, nil
}

// Uint64 reads eight bytes from the provided reader using a buffer from the
// free list, converts it to a number using the provided byte order, and returns
// the resulting uint64.
func (l binaryFreeList) Uint64(r io.Reader, byteOrder binary.ByteOrder) (uint64, error) {
	buf := l.Borrow()[:8]
	if _, err := io.ReadFull(r, buf); err != nil {
		l.Return(buf)
		return 0, err
	}
	rv := byteOrder.Uint64(buf)
	l.Return(buf)
	return rv, nil
}

// scriptFreeList defines a free list of byte slices (up to the maximum number
// defined by the freeListMaxItems constant) that have a cap according to the
// freeListMaxScriptSize constant.  It is used to provide temporary buffers for
// deserializing scripts in order to greatly reduce the number of allocations
// required.
//
// The caller can obtain a buffer from the free list by calling the Borrow
// function and should return it via the Return function when done using it.
type scriptFreeList chan []byte

// Return puts the provided byte slice back on the free list when it has a cap
// of the expected length.  The buffer is expected to have been obtained via
// the Borrow function.  Any slices that are not of the appropriate size, such
// as those whose size is greater than the largest allowed free list item size
// are simply ignored so they can go to the garbage collector.
func (c scriptFreeList) Return(buf []byte) {
	// Ignore any buffers returned that aren't the expected size for the
	// free list.
	if cap(buf) != 1000 {
		return
	}

	// Return the buffer to the free list when it's not full.  Otherwise let
	// it be garbage collected.
	select {
	case c <- buf:
	default:
		// Let it go to the garbage collector.
	}
}

// readTxIn reads the next sequence of bytes from r as a transaction input
// (TxIn).
func readTxIn(r io.Reader, pver uint32, version int32, ti *TxIn) error {
	err := readOutPoint(r, pver, version, &ti.PreviousOutPoint)
	if err != nil {
		return err
	}

	ti.SignatureScript, err = readScript(r, pver, 10000,
		"transaction input signature script")
	if err != nil {
		return err
	}

	return readElement(r, &ti.Sequence)
}

// readScript reads a variable length byte array that represents a transaction
// script.  It is encoded as a varInt containing the length of the array
// followed by the bytes themselves.  An error is returned if the length is
// greater than the passed maxAllowed parameter which helps protect against
// memory exhuastion attacks and forced panics thorugh malformed messages.  The
// fieldName parameter is only used for the error message so it provides more
// context in the error.
func readScript(r io.Reader, pver uint32, maxAllowed uint32, fieldName string) ([]byte, error) {
	count, err := ReadVarInt(r, pver)
	if err != nil {
		return nil, err
	}

	// Prevent byte array larger than the max message size.  It would
	// be possible to cause memory exhaustion and panics without a sane
	// upper bound on this count.
	if count > uint64(maxAllowed) {
		str := fmt.Sprintf("%s is larger than the max allowed size "+
			"[count %d, max %d]", fieldName, count, maxAllowed)
		return nil, errors.New(str)
	}

	b := scriptPool.Borrow(count)
	_, err = io.ReadFull(r, b)
	if err != nil {
		scriptPool.Return(b)
		return nil, err
	}
	return b, nil
}

// Borrow returns a byte slice from the free list with a length according the
// provided size.  A new buffer is allocated if there are any items available.
//
// When the size is larger than the max size allowed for items on the free list
// a new buffer of the appropriate size is allocated and returned.  It is safe
// to attempt to return said buffer via the Return function as it will be
// ignored and allowed to go the garbage collector.
func (c scriptFreeList) Borrow(size uint64) []byte {
	if size > 1000 {
		return make([]byte, size)
	}

	var buf []byte
	select {
	case buf = <-c:
	default:
		buf = make([]byte, 1000)
	}
	return buf[:size]
}

// readElement reads the next sequence of bytes from r using little endian
// depending on the concrete type of element pointed to.
func readElement(r io.Reader, element interface{}) error {
	// Attempt to read the element based on the concrete type via fast
	// type assertions first.
	switch e := element.(type) {
	case *int32:
		rv, err := binarySerializer.Uint32(r, littleEndian)
		if err != nil {
			return err
		}
		*e = int32(rv)
		return nil

	case *uint32:
		rv, err := binarySerializer.Uint32(r, littleEndian)
		if err != nil {
			return err
		}
		*e = rv
		return nil

	case *int64:
		rv, err := binarySerializer.Uint64(r, littleEndian)
		if err != nil {
			return err
		}
		*e = int64(rv)
		return nil

	case *uint64:
		rv, err := binarySerializer.Uint64(r, littleEndian)
		if err != nil {
			return err
		}
		*e = rv
		return nil

	case *bool:
		rv, err := binarySerializer.Uint8(r)
		if err != nil {
			return err
		}
		if rv == 0x00 {
			*e = false
		} else {
			*e = true
		}
		return nil

	// Unix timestamp encoded as a uint32.
	case *uint32Time:
		rv, err := binarySerializer.Uint32(r, binary.LittleEndian)
		if err != nil {
			return err
		}
		*e = uint32Time(time.Unix(int64(rv), 0))
		return nil

	// Unix timestamp encoded as an int64.
	case *int64Time:
		rv, err := binarySerializer.Uint64(r, binary.LittleEndian)
		if err != nil {
			return err
		}
		*e = int64Time(time.Unix(int64(rv), 0))
		return nil

	// Message header checksum.
	case *[4]byte:
		_, err := io.ReadFull(r, e[:])
		if err != nil {
			return err
		}
		return nil

	// IP address.
	case *[16]byte:
		_, err := io.ReadFull(r, e[:])
		if err != nil {
			return err
		}
		return nil

	case *chainhash.Hash:
		_, err := io.ReadFull(r, e[:])
		if err != nil {
			return err
		}
		return nil

	}

	// Fall back to the slower binary.Read if a fast path was not available
	// above.
	return binary.Read(r, littleEndian, element)
}

// uint32Time represents a unix timestamp encoded with a uint32.  It is used as
// a way to signal the readElement function how to decode a timestamp into a Go
// time.Time since it is otherwise ambiguous.
type uint32Time time.Time

// int64Time represents a unix timestamp encoded with an int64.  It is used as
// a way to signal the readElement function how to decode a timestamp into a Go
// time.Time since it is otherwise ambiguous.
type int64Time time.Time

// readOutPoint reads the next sequence of bytes from r as an OutPoint.
func readOutPoint(r io.Reader, pver uint32, version int32, op *OutPoint) error {
	_, err := io.ReadFull(r, op.Hash[:])
	if err != nil {
		return err
	}

	op.Index, err = binarySerializer.Uint32(r, littleEndian)
	return err
}

// Create the concurrent safe free list to use for script deserialization.  As
// previously described, this free list is maintained to significantly reduce
// the number of allocations.
var scriptPool scriptFreeList = make(chan []byte, 1000)

// readTxOut reads the next sequence of bytes from r as a transaction output
// (TxOut).
func readTxOut(r io.Reader, pver uint32, version int32, to *TxOut) error {
	err := readElement(r, &to.Value)
	if err != nil {
		return err
	}

	to.PkScript, err = readScript(r, pver, 10000,
		"transaction output public key script")
	return err
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// This is part of the Message interface implementation.
// See Deserialize for decoding transactions stored to disk, such as in a
// database, as opposed to decoding transactions from the wire.
func (msg *MsgTx) BtcDecode(r io.Reader, pver uint32) error {

	version, err := binarySerializer.Uint32(r, littleEndian)
	if err != nil {
		return err
	}
	msg.Version = int32(version)

	count, err := ReadVarInt(r, pver)
	if err != nil {
		return err
	}

	// A count of zero (meaning no TxIn's to the uninitiated) indicates
	// this is a transaction with witness data.
	var flag [1]byte
	if count == 0 {
		// Next, we need to read the flag, which is a single byte.
		if _, err = io.ReadFull(r, flag[:]); err != nil {
			return err
		}

		// At the moment, the flag MUST be 0x01. In the future other
		// flag types may be supported.
		if flag[0] != 0x01 {
			str := fmt.Sprintf("witness tx but flag byte is %x", flag)
			panic(str)
		}

		// With the Segregated Witness specific fields decoded, we can
		// now read in the actual txin count.
		count, err = ReadVarInt(r, pver)
		if err != nil {
			return err
		}
	}

  maxTxInPerMessage := 1000

	// Prevent more input transactions than could possibly fit into a
	// message.  It would be possible to cause memory exhaustion and panics
	// without a sane upper bound on this count.
	if count > uint64(maxTxInPerMessage) {
		str := fmt.Sprintf("too many input transactions to fit into "+
			"max message size [count %d, max %d]", count,
			maxTxInPerMessage)
		panic(str)
	}

	// returnScriptBuffers is a closure that returns any script buffers that
	// were borrowed from the pool when there are any deserialization
	// errors.  This is only valid to call before the final step which
	// replaces the scripts with the location in a contiguous buffer and
	// returns them.
	returnScriptBuffers := func() {
		for _, txIn := range msg.TxIn {
			if txIn == nil {
				continue
			}

			if txIn.SignatureScript != nil {
				scriptPool.Return(txIn.SignatureScript)
			}

			for _, witnessElem := range txIn.Witness {
				if witnessElem != nil {
					scriptPool.Return(witnessElem)
				}
			}
		}
		for _, txOut := range msg.TxOut {
			if txOut == nil || txOut.PkScript == nil {
				continue
			}
			scriptPool.Return(txOut.PkScript)
		}
	}

	// Deserialize the inputs.
	var totalScriptSize uint64
	txIns := make([]TxIn, count)
	msg.TxIn = make([]*TxIn, count)
	for i := uint64(0); i < count; i++ {
		// The pointer is set now in case a script buffer is borrowed
		// and needs to be returned to the pool on error.
		ti := &txIns[i]
		msg.TxIn[i] = ti
		err = readTxIn(r, pver, msg.Version, ti)
		if err != nil {
			returnScriptBuffers()
			return err
		}
		totalScriptSize += uint64(len(ti.SignatureScript))
	}

	count, err = ReadVarInt(r, pver)
	if err != nil {
		returnScriptBuffers()
		return err
	}

	// Prevent more output transactions than could possibly fit into a
	// message.  It would be possible to cause memory exhaustion and panics
	// without a sane upper bound on this count.
	if count > uint64(1000) {
		returnScriptBuffers()
		str := fmt.Sprintf("too many output transactions to fit into "+
			"max message size [count %d, max %d]", count,
			1000)
		return errors.New(str)
	}

	// Deserialize the outputs.
	txOuts := make([]TxOut, count)
	msg.TxOut = make([]*TxOut, count)
	for i := uint64(0); i < count; i++ {
		// The pointer is set now in case a script buffer is borrowed
		// and needs to be returned to the pool on error.
		to := &txOuts[i]
		msg.TxOut[i] = to
		err = readTxOut(r, pver, msg.Version, to)
		if err != nil {
			returnScriptBuffers()
			return err
		}
		totalScriptSize += uint64(len(to.PkScript))
	}

	// If the transaction's flag byte isn't 0x00 at this point, then one or
	// more of its inputs has accompanying witness data.
	if flag[0] != 0 {
		for _, txin := range msg.TxIn {
			// For each input, the witness is encoded as a stack
			// with one or more items. Therefore, we first read a
			// varint which encodes the number of stack items.
			witCount, err := ReadVarInt(r, pver)
			if err != nil {
				returnScriptBuffers()
				return err
			}

			// Prevent a possible memory exhaustion attack by
			// limiting the witCount value to a sane upper bound.
			if witCount > 100 {
				returnScriptBuffers()
				str := fmt.Sprintf("too many witness items to fit "+
					"into max message size [count %d, max %d]",
					witCount, 100)
				return errors.New(str)
			}

			// Then for witCount number of stack items, each item
			// has a varint length prefix, followed by the witness
			// item itself.
			txin.Witness = make([][]byte, witCount)
			for j := uint64(0); j < witCount; j++ {
				txin.Witness[j], err = readScript(r, pver,
					1000, "script witness item")
				if err != nil {
					returnScriptBuffers()
					return err
				}
				totalScriptSize += uint64(len(txin.Witness[j]))
			}
		}
	}

	msg.LockTime, err = binarySerializer.Uint32(r, littleEndian)
	if err != nil {
		returnScriptBuffers()
		return err
	}

	// Create a single allocation to house all of the scripts and set each
	// input signature script and output public key script to the
	// appropriate subslice of the overall contiguous buffer.  Then, return
	// each individual script buffer back to the pool so they can be reused
	// for future deserializations.  This is done because it significantly
	// reduces the number of allocations the garbage collector needs to
	// track, which in turn improves performance and drastically reduces the
	// amount of runtime overhead that would otherwise be needed to keep
	// track of millions of small allocations.
	//
	// NOTE: It is no longer valid to call the returnScriptBuffers closure
	// after these blocks of code run because it is already done and the
	// scripts in the transaction inputs and outputs no longer point to the
	// buffers.
	var offset uint64
	scripts := make([]byte, totalScriptSize)
	for i := 0; i < len(msg.TxIn); i++ {
		// Copy the signature script into the contiguous buffer at the
		// appropriate offset.
		signatureScript := msg.TxIn[i].SignatureScript
		copy(scripts[offset:], signatureScript)

		// Reset the signature script of the transaction input to the
		// slice of the contiguous buffer where the script lives.
		scriptSize := uint64(len(signatureScript))
		end := offset + scriptSize
		msg.TxIn[i].SignatureScript = scripts[offset:end:end]
		offset += scriptSize

		// Return the temporary script buffer to the pool.
		scriptPool.Return(signatureScript)

		for j := 0; j < len(msg.TxIn[i].Witness); j++ {
			// Copy each item within the witness stack for this
			// input into the contiguous buffer at the appropriate
			// offset.
			witnessElem := msg.TxIn[i].Witness[j]
			copy(scripts[offset:], witnessElem)

			// Reset the witness item within the stack to the slice
			// of the contiguous buffer where the witness lives.
			witnessElemSize := uint64(len(witnessElem))
			end := offset + witnessElemSize
			msg.TxIn[i].Witness[j] = scripts[offset:end:end]
			offset += witnessElemSize

			// Return the temporary buffer used for the witness stack
			// item to the pool.
			scriptPool.Return(witnessElem)
		}
	}
	for i := 0; i < len(msg.TxOut); i++ {
		// Copy the public key script into the contiguous buffer at the
		// appropriate offset.
		pkScript := msg.TxOut[i].PkScript
		copy(scripts[offset:], pkScript)

		// Reset the public key script of the transaction output to the
		// slice of the contiguous buffer where the script lives.
		scriptSize := uint64(len(pkScript))
		end := offset + scriptSize
		msg.TxOut[i].PkScript = scripts[offset:end:end]
		offset += scriptSize

		// Return the temporary script buffer to the pool.
		scriptPool.Return(pkScript)
	}

	return nil
}

// DeriveRevocationRoot derives an root unique to a channel given the
// derivation root, and the blockhash that the funding process began at and the
// remote node's identity public key. The seed is derived using the HKDF[1][2]
// instantiated with sha-256. With this schema, once we know the block hash of
// the funding transaction, and who we funded the channel with, we can
// reconstruct all of our revocation state.
//
// [1]: https://eprint.iacr.org/2010/264.pdf
// [2]: https://tools.ietf.org/html/rfc5869
func DeriveRevocationRoot(derivationRoot *btcec.PrivateKey,
	blockSalt chainhash.Hash, nodePubKey *btcec.PublicKey) chainhash.Hash {

	secret := derivationRoot.Serialize()
	salt := blockSalt[:]
	info := nodePubKey.SerializeCompressed()

	seedReader := hkdf.New(sha256.New, secret, salt, info)

	// It's safe to ignore the error her as we know for sure that we won't
	// be draining the HKDF past its available entropy horizon.
	// TODO(roasbeef): revisit...
	var root chainhash.Hash
	seedReader.Read(root[:])

	return root
}

// Deserialize decodes a transaction from r into the receiver using a format
// that is suitable for long-term storage such as a database while respecting
// the Version field in the transaction.  This function differs from BtcDecode
// in that BtcDecode decodes from the bitcoin wire protocol as it was sent
// across the network.  The wire encoding can technically differ depending on
// the protocol version and doesn't even really need to match the format of a
// stored transaction at all.  As of the time this comment was written, the
// encoded transaction is the same in both instances, but there is a distinct
// difference and separating the two allows the API to be flexible enough to
// deal with changes.
func (msg *MsgTx) Deserialize(r io.Reader) error {
	// At the current time, there is no difference between the wire encoding
	// at protocol version 0 and the stable long-term storage format.  As
	// a result, make use of BtcDecode.
	return msg.BtcDecode(r, 0)
}

// index is a number which identifies the hash number and serves as a way to
// determine the hashing operation required  to derive one hash from another.
// index is initialized with the startIndex and decreases down to zero with
// successive derivations.
type index uint64

// element represents the entity which contains the hash and index
// corresponding to it. An element is the output of the shachain PRF. By
// comparing two indexes we're able to mutate the hash in such way to derive
// another element.
type element struct {
	index index
	hash  chainhash.Hash
}

// RevocationProducer is an implementation of Producer interface using the
// shachain PRF construct. Starting with a single 32-byte element generated
// from a CSPRNG, shachain is able to efficiently generate a nearly unbounded
// number of secrets while maintaining a constant amount of storage. The
// original description of shachain can be found here:
// https://github.com/rustyrussell/ccan/blob/master/ccan/crypto/shachain/design.txt
// with supplementary material here:
// https://github.com/lightningnetwork/lightning-rfc/blob/master/03-transactions.md#per-commitment-secret-requirements
type RevocationProducer struct {
	// root is the element from which we may generate all hashes which
	// corresponds to the index domain [281474976710655,0].
	root *element
}

const (
	// maxHeight is used to determine the maximum allowable index and the
	// length of the array required to order to derive all previous hashes
	// by index. The entries of this array as also knowns as buckets.
	maxHeight uint8 = 48

	// rootIndex is an index which corresponds to the root hash.
	rootIndex index = 0
)

// NewRevocationProducer creates new instance of shachain producer.
func NewRevocationProducer(root chainhash.Hash) *RevocationProducer {
	return &RevocationProducer{
		root: &element{
			index: rootIndex,
			hash:  root,
		}}
}

// startIndex is the index of first element in the shachain PRF.
var startIndex index = (1 << maxHeight) - 1

// newIndex is used to create index instance. The inner operations with index
// implies that index decreasing from some max number to zero, but for
// simplicity and backward compatibility with previous logic it was transformed
// to work in opposite way.
func newIndex(v uint64) index {
	return startIndex - index(v)
}

// deriveBitTransformations function checks that the 'to' index is derivable
// from the 'from' index by checking the indexes are prefixes of another. The
// bit positions where the zeroes should be changed to ones in order for the
// indexes to become the same are returned. This set of bits is needed in order
// to derive one hash from another.
//
// NOTE: The index 'to' is derivable from index 'from' iff index 'from' lies
// left and above index 'to' on graph below, for example:
// 1. 7(0b111) -> 7
// 2. 6(0b110) -> 6,7
// 3. 5(0b101) -> 5
// 4. 4(0b100) -> 4,5,6,7
// 5. 3(0b011) -> 3
// 6. 2(0b010) -> 2, 3
// 7. 1(0b001) -> 1
//
//    ^ bucket number
//    |
//  3 |   x
//    |   |
//  2 |   |               x
//    |   |               |
//  1 |   |       x       |       x
//    |   |       |       |       |
//  0 |   |   x   |   x   |   x   |   x
//    |   |   |   |   |   |   |   |   |
//    +---|---|---|---|---|---|---|---|---> index
//        0   1   2   3   4   5   6   7
//
func (from index) deriveBitTransformations(to index) ([]uint8, error) {
	var positions []uint8

	if from == to {
		return positions, nil
	}

	//	+ --------------- +
	// 	| №  | from | to  |
	//	+ -- + ---- + --- +
	//	| 48 |	 1  |  1  |
	//	| 47 |	 0  |  0  | [48-5] - same part of 'from' and 'to'
	//	| 46 |   0  |  0  |	    indexes which also is called prefix.
	//		....
	//	|  5 |	 1  |  1  |
	//	|  4 |	 0  |  1  | <--- position after which indexes becomes
	//	|  3 |   0  |  0  |	 different, after this position
	//	|  2 |   0  |  1  |	 bits in 'from' index all should be
	//	|  1 |   0  |  0  |	 zeros or such indexes considered to be
	//	|  0 |   0  |  1  |	 not derivable.
	//	+ -- + ---- + --- +
	zeros := countTrailingZeros(from)
	if uint64(from) != getPrefix(to, zeros) {
		return nil, errors.New("prefixes are different - indexes " +
			"aren't derivable")
	}

	// The remaining part of 'to' index represents the positions which we
	// will use then in order to derive one element from another.
	for position := zeros - 1; ; position-- {
		if getBit(to, position) == 1 {
			positions = append(positions, position)
		}

		if position == 0 {
			break
		}
	}

	return positions, nil
}

// countTrailingZeros count number of of trailing zero bits, this function is
// used to determine the number of element bucket.
func countTrailingZeros(index index) uint8 {
	var zeros uint8
	for ; zeros < maxHeight; zeros++ {

		if getBit(index, zeros) != 0 {
			break
		}
	}

	return zeros
}

// getBit return bit on index at position.
func getBit(index index, position uint8) uint8 {
	return uint8((uint64(index) >> position) & 1)
}

func getPrefix(index index, position uint8) uint64 {
	//	+ -------------------------- +
	// 	| №  | value | mask | return |
	//	+ -- + ----- + ---- + ------ +
	//	| 63 |	 1   |  0   |	 0   |
	//	| 62 |	 0   |  0   |	 0   |
	//	| 61 |   1   |  0   |	 0   |
	//		....
	//	|  4 |	 1   |  0   |	 0   |
	//	|  3 |   1   |  0   |	 0   |
	//	|  2 |   1   |  1   |	 1   | <--- position
	//	|  1 |   0   |  1   |	 0   |
	//	|  0 |   1   |  1   |	 1   |
	//	+ -- + ----- + ---- + ------ +

	var zero uint64
	mask := (zero - 1) - uint64((1<<position)-1)
	return (uint64(index) & mask)
}

// derive computes one shachain element from another by applying a series of
// bit flips and hasing operations based on the starting and ending index.
func (e *element) derive(toIndex index) (*element, error) {
	fromIndex := e.index

	positions, err := fromIndex.deriveBitTransformations(toIndex)
	if err != nil {
		return nil, err
	}

	buf := e.hash.CloneBytes()
	for _, position := range positions {
		// Flip the bit and then hash the current state.
		byteNumber := position / 8
		bitNumber := position % 8

		buf[byteNumber] ^= (1 << bitNumber)

		h := sha256.Sum256(buf)
		buf = h[:]
	}

	hash, err := chainhash.NewHash(buf)
	if err != nil {
		return nil, err
	}

	return &element{
		index: toIndex,
		hash:  *hash,
	}, nil
}

// AtIndex produces a secret by evaluating using the initial seed and a
// particular index.
//
// NOTE: Part of the Producer interface.
func (p *RevocationProducer) AtIndex(v uint64) (*chainhash.Hash, error) {
	ind := newIndex(v)

	element, err := p.root.derive(ind)
	if err != nil {
		return nil, err
	}

	return &element.hash, nil
}

func TestKeyDerivation(t *testing.T) {

ROOTKEY := []byte{5, 194, 36, 163, 70, 50, 245, 165, 153, 51, 143, 105, 175, 15, 138, 56, 4, 184, 42, 142, 206, 29, 9, 188, 241, 138, 56, 41, 173, 113, 130, 158}
rot, _ := btcec.PrivKeyFromBytes(btcec.S256(), ROOTKEY)
raw := rot.Serialize()
//fmt.Println(raw)
master, err := hdkeychain.NewMaster(raw, &chaincfg.SimNetParams)
if err != nil {
  panic(err)
}
//# https://github.com/lightningnetwork/lnd/blob/0377a4f99de4a533eaffafcda6e47354db566be2/lnwallet/wallet.go#L33

masterRevocationRoot, err2 := master.Child(0x80000000 + 1)
if err2 != nil {
  panic(err2)
}
masterElkremRoot, err3 := masterRevocationRoot.ECPrivKey()
if err3 != nil {
  panic(err3)
}
ser := masterElkremRoot.Serialize()
if (!reflect.DeepEqual([]byte {134 ,120 ,57 ,103 ,89 ,81 ,195 ,251 ,249 ,182 ,62 ,146 ,179 ,0 ,247 ,191 ,96 ,181 ,137 ,150 ,181 ,102 ,72 ,70 ,127 ,149 ,108 ,84 ,139 ,235 ,93 ,143} , ser)) {
  fmt.Println(ser)
  panic(ser)
}

bestHash, _ := chainhash.NewHashFromStr("709e049390543cf221001ab4f4ed09186672331713193313ebb11e203f0b6a72")

nodeID, errpub := btcec.ParsePubKey([]byte {4 ,67 ,192 ,254 ,221 ,251 ,89 ,162 ,161 ,75 ,221 ,54 ,87 ,137 ,93 ,59 ,2 ,177 ,219 ,28 ,154 ,145 ,53 ,203 ,227 ,248 ,141 ,252 ,76 ,225 ,10 ,154 ,86 ,254 ,20 ,110 ,107 ,92 ,162 ,34 ,98 ,231 ,24 ,238 ,212 ,44 ,238 ,246 ,187 ,14 ,199 ,61 ,39 ,237 ,202 ,252 ,161 ,166 ,58 ,50 ,239 ,53 ,102 ,16 ,65}, btcec.S256())
if errpub != nil {
  panic("bad pubkey")
}

revocationRoot := DeriveRevocationRoot(masterElkremRoot, *bestHash,
		nodeID)

correctrevoroot, err5 := chainhash.NewHashFromStr("389a1b6184a77d98bb7f91cb89e681083d0c30325cc19fd9709df762b90be7f3")

if err5 != nil || revocationRoot != *correctrevoroot {
  panic("revocation root wrong")
}

	// Once we have the root, we can then generate our shachain producer
	// and from that generate the per-commitment point.
	producer := NewRevocationProducer(revocationRoot)
	firstPreimage, err := producer.AtIndex(0)

correct1stpreimage, preimageerr := chainhash.NewHashFromStr("52eb5824eab69e5a1e85ba2b5a98258db140483ee9c6307cf77446b9384fd131")
if preimageerr != nil {
  panic(preimageerr)
}

if firstPreimage.String() != correct1stpreimage.String() {
  panic(fmt.Sprintf("first preimage wrong %v", firstPreimage))
}
}


func TestComputeInputScript(t *testing.T) {

SERIALIZEDTXANDSIGNDESC := []byte{0,125,1,0,0,0,1,211,171,158,20,224,139,71,93,77,94,74,135,98,70,57,191,79,168,32,75,105,235,123,26,235,34,171,178,162,172,163,27,1,0,0,0,0,255,255,255,255,2,16,39,0,0,0,0,0,0,34,0,32,241,106,220,228,192,219,245,222,136,170,106,154,87,194,100,111,30,116,37,236,5,105,182,73,206,51,218,34,151,46,170,28,224,149,11,84,2,0,0,0,22,0,20,145,192,229,78,173,112,91,95,249,249,254,9,53,182,97,159,113,210,165,116,0,0,0,0,0,210,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,2,84,11,228,0,0,23,169,20,128,199,54,81,52,79,130,219,224,213,226,92,93,27,6,153,193,105,11,130,135,0,0,0,1,217,43,73,76,147,7,70,63,188,219,20,47,234,97,195,13,216,87,117,11,107,76,81,144,254,102,255,191,72,130,26,154,59,177,48,41,206,123,31,85,158,245,231,71,252,172,67,159,20,85,162,236,124,95,9,183,34,144,121,94,112,102,80,68,49,104,166,12,84,134,136,136,201,54,92,173,174,23,215,5,206,240,150,172,65,238,5,213,166,63,170,11,195,67,37,187,0,0,0,0,0,0,0,0}
txlen := int(binary.BigEndian.Uint16(SERIALIZEDTXANDSIGNDESC[:2]))
if txlen != 125 {
  panic("wrong len")
}

var tx MsgTx
err0 := tx.Deserialize(bytes.NewReader(SERIALIZEDTXANDSIGNDESC[2:2+txlen]))
if err0 != nil {
  panic("deserialize tx err")
}
signdesc := SERIALIZEDTXANDSIGNDESC[2+txlen:]

leng := int(binary.BigEndian.Uint16(signdesc[:2]))
signdesc = signdesc[2:]
if leng != len(signdesc) {
  panic("length wrong")
}

pubkey := signdesc[:33]
signdesc = signdesc[33:]

singletweaklen := uint(binary.BigEndian.Uint16(signdesc[:2]))
signdesc = signdesc[2:]

singletweak := signdesc[:singletweaklen]
signdesc = signdesc[singletweaklen:]

doubletweaklen := 32

doubletweak := signdesc[:doubletweaklen]
signdesc = signdesc[doubletweaklen:]

witnessscriptlen := uint(binary.BigEndian.Uint16(signdesc[:2]))
signdesc = signdesc[2:]

witnessscript := signdesc[:witnessscriptlen]
signdesc = signdesc[witnessscriptlen:]

outputvalue := int64(binary.BigEndian.Uint64(signdesc[:8]))
signdesc = signdesc[8:]

outputpkscriptlen := uint(binary.BigEndian.Uint16(signdesc[:2]))
signdesc = signdesc[2:]

outputpkscript := signdesc[:outputpkscriptlen]
signdesc = signdesc[outputpkscriptlen:]

hashtype := int32(binary.BigEndian.Uint32(signdesc[:4]))
signdesc = signdesc[4:]

hashprevouts := signdesc[:32]
signdesc = signdesc[32:]

hashsequence := signdesc[:32]
signdesc = signdesc[32:]

hashoutputs := signdesc[:32]
signdesc = signdesc[32:]

inputindex := int(binary.BigEndian.Uint64(signdesc[:8]))
signdesc = signdesc[8:]

cpubkey := pubkey[1:]
var val uint64
val = uint64(byte(pubkey[0]))

for {
  if len(cpubkey) == 0 {
    break
  }
  val = val | binary.BigEndian.Uint64(cpubkey[:8])
  cpubkey = cpubkey[8:]
}
var pubkeyobj *btcec.PublicKey
if val != 0 {
  pubkeyobj, err0 = btcec.ParsePubKey(pubkey, btcec.S256())
  if err0 != nil {
    panic(fmt.Sprintf("err2 %v msg: %v", pubkey, err0))
  }
}
doubletweakpriv, _ := btcec.PrivKeyFromBytes(btcec.S256(), doubletweak)

if len(signdesc) != 0 {
  panic(fmt.Sprintf("signdesc not empty %v", len(signdesc)))
}



//WALLETADDR &{0xc4204fa000 0 0xc42049b560 false false true false 4 0xc420526e70 [73 206 156 146 252 111 76 201 12 225 213 151 132 170 175 98 65 145 56 145 74 187 15 198 171 40 111 48 110 153 229 222 107 62 184 21 199 249 239 21 110 254 247 177 103 229 120 45 106 233 127 166 165 115 39 247 64 220 213 243 252 185 146 158 69 11 209 31 227 213 157 126] [227 0 92 166 142 91 9 127 71 60 232 138 62 20 208 83 191 85 135 108 94 243 108 105 17 47 130 70 14 222 55 7] {0 0}}
PRIVKEY := []byte{227,0,92,166,142,91,9,127,71,60,232,138,62,20,208,83,191,85,135,108,94,243,108,105,17,47,130,70,14,222,55,7}
pri, _ := btcec.PrivKeyFromBytes(btcec.S256(), PRIVKEY)
INPUTSCRIPTSCRIPTSIG := []byte{ 22,0,20,157,152,5,36,153,45,228,145,20,188,199,140,125,173,247,140,169,123,131,107}
INPUTSCRIPTWITNESS0 := [][]byte { []byte {48,68,2,32,62,85,194,71,180,244,2,87,141,53,208,147,25,47,181,82,25,88,118,216,45,70,168,14,65,144,142,71,205,4,105,209,2,32,17,185,10,179,229,150,236,161,45,49,199,206,16,79,105,228,13,185,39,231,184,62,199,137,80,190,249,211,70,248,95,40,1},
 []byte{2,152,114,200,214,173,54,35,226,31,183,214,244,94,192,247,57,2,88,88,67,25,135,209,219,147,55,211,41,252,217,54,52}}
inp := InputScript{ScriptSig:INPUTSCRIPTSCRIPTSIG, Witness:INPUTSCRIPTWITNESS0}
h1, err4 := chainhash.NewHash(hashprevouts)
h2, err5 := chainhash.NewHash(hashoutputs)
h3, err6 := chainhash.NewHash(hashsequence)

if err4 != nil {panic("hash err")}
if err5 != nil {panic("hash err")}
if err6 != nil {panic("hash err")}
result, err := ComputeInputScript(&tx, &SignDescriptor {
	PubKey: pubkeyobj,
	SingleTweak: singletweak,
	DoubleTweak: doubletweakpriv,
	WitnessScript: witnessscript,
	Output: &TxOut{PkScript: outputpkscript, Value:outputvalue},
	HashType: SigHashType(hashtype),
	SigHashes: &TxSigHashes {HashPrevOuts: *h1, HashOutputs: *h2, HashSequence: *h3},
	InputIndex: inputindex,
}, pri)

if err != nil {
  panic(fmt.Sprintf("ComputeInputScriptResult errored %v", err))
}

if !reflect.DeepEqual(inp, *result) {
  panic("WRONG ComputeInputScriptResult")
}
fmt.Println("all ok!\n")
}

// ComputeInputScript generates a complete InputIndex for the passed
// transaction with the signature as defined within the passed SignDescriptor.
// This method is capable of generating the proper input script for both
// regular p2wkh output and p2wkh outputs nested within a regular p2sh output.
//
// This is a part of the WalletController interface.
func ComputeInputScript(tx *MsgTx,
	signDesc *SignDescriptor,  privKey *btcec.PrivateKey) (*InputScript, error) {

	outputScript := signDesc.Output.PkScript

	isNestedWitness := true

	var witnessProgram []byte
	inputScript := &InputScript{}

	switch {

	// If we're spending p2wkh output nested within a p2sh output, then
	// we'll need to attach a sigScript in addition to witness data.
	case isNestedWitness:
		pubKey := privKey.PubKey()
		pubKeyHash := btcutil.Hash160(pubKey.SerializeCompressed())

		// Next, we'll generate a valid sigScript that'll allow us to
		// spend the p2sh output. The sigScript will contain only a
		// single push of the p2wkh witness program corresponding to
		// the matching public key of this address.
		p2wkhAddr, err := btcutil.NewAddressWitnessPubKeyHash(pubKeyHash,
			&chaincfg.SimNetParams)
		if err != nil {
			return nil, err
		}
		witnessProgram, err = txscript.PayToAddrScript(p2wkhAddr)
		if err != nil {
			return nil, err
		}

		bldr := txscript.NewScriptBuilder()
		bldr.AddData(witnessProgram)
		sigScript, err := bldr.Script()
		if err != nil {
			return nil, err
		}

		inputScript.ScriptSig = sigScript

	// Otherwise, this is a regular p2wkh output, so we include the
	// witness program itself as the subscript to generate the proper
	// sighash digest. As part of the new sighash digest algorithm, the
	// p2wkh witness program will be expanded into a regular p2kh
	// script.
	default:
		witnessProgram = outputScript
	}

  var err error

	// If a tweak (single or double) is specified, then we'll need to use
	// this tweak to derive the final private key to be used for signing
	// this output.
	privKey, err = maybeTweakPrivKey(signDesc, privKey)
	if err != nil {
		return nil, err
	}

	// Generate a valid witness stack for the input.
	// TODO(roasbeef): adhere to passed HashType
	witnessScript, err := WitnessSignature(tx, signDesc.SigHashes,
		signDesc.InputIndex, signDesc.Output.Value, witnessProgram,
		SigHashAll, privKey, true)
	if err != nil {
		return nil, err
	}

	inputScript.Witness = witnessScript

	return inputScript, nil
}

// SingleTweakBytes computes set of bytes we call the single tweak. The purpose
// of the single tweak is to randomize all regular delay and payment base
// points. To do this, we generate a hash that binds the commitment point to
// the pay/delay base point. The end end results is that the basePoint is
// tweaked as follows:
//
//   * key = basePoint + sha256(commitPoint || basePoint)*G
func SingleTweakBytes(commitPoint, basePoint *btcec.PublicKey) []byte {
	h := sha256.New()
	h.Write(commitPoint.SerializeCompressed())
	h.Write(basePoint.SerializeCompressed())
	return h.Sum(nil)
}

// DeriveRevocationPrivKey derives the revocation private key given a node's
// commitment private key, and the preimage to a previously seen revocation
// hash. Using this derived private key, a node is able to claim the output
// within the commitment transaction of a node in the case that they broadcast
// a previously revoked commitment transaction.
//
// The private key is derived as follwos:
//   revokePriv := (revokeBasePriv * sha256(revocationBase || commitPoint)) +
//                 (commitSecret * sha256(commitPoint || revocationBase)) mod N
//
// Where N is the order of the sub-group.
func DeriveRevocationPrivKey(revokeBasePriv *btcec.PrivateKey,
	commitSecret *btcec.PrivateKey) *btcec.PrivateKey {

	// r = sha256(revokeBasePub || commitPoint)
	revokeTweakBytes := SingleTweakBytes(revokeBasePriv.PubKey(),
		commitSecret.PubKey())
	revokeTweakInt := new(big.Int).SetBytes(revokeTweakBytes)

	// c = sha256(commitPoint || revokeBasePub)
	commitTweakBytes := SingleTweakBytes(commitSecret.PubKey(),
		revokeBasePriv.PubKey())
	commitTweakInt := new(big.Int).SetBytes(commitTweakBytes)

	// Finally to derive the revocation secret key we'll perform the
	// following operation:
	//
	//  k = (revocationPriv * r) + (commitSecret * c) mod N
	//
	// This works since:
	//  P = (G*a)*b + (G*c)*d
	//  P = G*(a*b) + G*(c*d)
	//  P = G*(a*b + c*d)
	revokeHalfPriv := revokeTweakInt.Mul(revokeTweakInt, revokeBasePriv.D)
	commitHalfPriv := commitTweakInt.Mul(commitTweakInt, commitSecret.D)

	revocationPriv := revokeHalfPriv.Add(revokeHalfPriv, commitHalfPriv)
	revocationPriv = revocationPriv.Mod(revocationPriv, btcec.S256().N)

	priv, _ := btcec.PrivKeyFromBytes(btcec.S256(), revocationPriv.Bytes())
	return priv
}

// TweakPrivKey tweaks the private key of a public base point given a per
// commitment point. The per commitment secret is the revealed revocation
// secret for the commitment state in question. This private key will only need
// to be generated in the case that a channel counter party broadcasts a
// revoked state. Precisely, the following operation is used to derive a
// tweaked private key:
//
//  * tweakPriv := basePriv + sha256(commitment || basePub) mod N
//
// Where N is the order of the sub-group.
func TweakPrivKey(basePriv *btcec.PrivateKey, commitTweak []byte) *btcec.PrivateKey {
	// tweakInt := sha256(commitPoint || basePub)
	tweakInt := new(big.Int).SetBytes(commitTweak)

	tweakInt = tweakInt.Add(tweakInt, basePriv.D)
	tweakInt = tweakInt.Mod(tweakInt, btcec.S256().N)

	tweakPriv, _ := btcec.PrivKeyFromBytes(btcec.S256(), tweakInt.Bytes())
	return tweakPriv
}
