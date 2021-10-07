package arbos

import (
	"bytes"
	"github.com/andybalholm/brotli"
	"github.com/ethereum/go-ethereum/common"
	"io"
	"math/big"
)

const (
	L1MessageType_L2Message             = 3
	L1MessageType_SetChainParams        = 4
	L1MessageType_EndOfBlock            = 6
	L1MessageType_L2FundedByL1          = 7
	L1MessageType_SubmitRetryable       = 9
	L1MessageType_BatchForGasEstimation = 10   // probably won't use this in practice
)

const MaxL2MessageSize = 256*1024

type IncomingMessage interface {
	handle(state *ArbosState)
}

type L1IncomingMessageHeader struct {
	kind        uint8
	sender      common.Address
	blockNumber common.Hash
	timestamp   common.Hash
	requestId   common.Hash
	gasPriceL1  common.Hash
}

type L1IncomingMessage struct {
	header *L1IncomingMessageHeader
	l2msg  []byte
}

func (msg *L1IncomingMessage) Serialize() ([]byte, error) {
	wr := &bytes.Buffer{}
	if err := wr.WriteByte(msg.header.kind); err != nil {
		return nil, err
	}

	if err := AddressTo256ToWriter(msg.header.sender, wr); err != nil {
		return nil, err
	}

	if err := HashToWriter(msg.header.blockNumber, wr); err != nil {
		return nil, err
	}

	if err := HashToWriter(msg.header.timestamp, wr); err != nil {
		return nil, err
	}

	if err := HashToWriter(msg.header.requestId, wr); err != nil {
		return nil, err
	}

	if err := HashToWriter(msg.header.gasPriceL1, wr); err != nil {
		return nil, err
	}

	if _, err := wr.Write(msg.l2msg); err != nil {
		return nil, err
	}

	return wr.Bytes(), nil
}

func (msg *L1IncomingMessage) Equals(other *L1IncomingMessage) bool {
	return msg.header.Equals(other.header) && (bytes.Compare(msg.l2msg, other.l2msg) == 0)
}

func (header *L1IncomingMessageHeader) Equals(other *L1IncomingMessageHeader) bool {
	return (header.kind == other.kind) &&
		(header.sender.Hash().Big().Cmp(other.sender.Hash().Big()) == 0) &&
		(header.blockNumber.Big().Cmp(other.blockNumber.Big()) == 0) &&
		(header.timestamp.Big().Cmp(other.timestamp.Big()) == 0) &&
		(header.requestId.Big().Cmp(other.requestId.Big()) == 0) &&
		(header.gasPriceL1.Big().Cmp(other.gasPriceL1.Big()) == 0)
}

func ParseIncomingL1Message(rd io.Reader, state *ArbosState) ([]MessageSegment, error) {
	var kindBuf [1]byte
	_, err := rd.Read(kindBuf[:])
	if err != nil {
		return nil, err
	}

	sender, err := AddressFrom256FromReader(rd)
	if err != nil {
		return nil, err
	}

	blockNumber, err := HashFromReader(rd)
	if err != nil {
		return nil, err
	}

	timestamp, err := HashFromReader(rd)
	if err != nil {
		return nil, err
	}

	requestId, err := HashFromReader(rd)
	if err != nil {
		return nil, err
	}

	gasPriceL1, err := HashFromReader(rd)
	if err != nil {
		return nil, err
	}

	data, err := io.ReadAll(rd)
	if err != nil {
		return nil, err
	}

	msg := &L1IncomingMessage{
		&L1IncomingMessageHeader{
			kindBuf[0],
			sender,
			blockNumber,
			timestamp,
			requestId,
			gasPriceL1,
		},
		data,
	}
	return msg.handle(state), nil
}

func (msg *L1IncomingMessage) handle(state *ArbosState) []MessageSegment {
	if len(msg.l2msg) > MaxL2MessageSize {
		// ignore the message if l2msg is too large
		return []MessageSegment{}
	}
	switch msg.header.kind {
	case L1MessageType_L2Message:
		return parseL2Message(bytes.NewReader(msg.l2msg), []MessageSegment{}, msg.header, true, state)
	case L1MessageType_SetChainParams:
		panic("unimplemented")
	case L1MessageType_EndOfBlock:
		panic("unimplemented")
	case L1MessageType_L2FundedByL1:
		panic("unimplemented")
	case L1MessageType_SubmitRetryable:
		panic("unimplemented")
	case L1MessageType_BatchForGasEstimation:
		panic("unimplemented")
	default:
		// invalid message, just ignore it
		return []MessageSegment{}
	}
}

const (
	L2MessageKind_UnsignedUserTx = 0
	L2MessageKind_ContractTx = 1
	L2MessageKind_NonmutatingCall = 2
	L2MessageKind_Batch = 3
	L2MessageKind_SignedTx = 4
	// 5 is reserved
	L2MessageKind_Heartbeat = 6
	L2MessageKind_SignedCompressedTx = 7
	// 8 is reserved for BLS signed batch
	L2MessageKind_BrotliCompressed = 8

)

func parseL2Message(rd io.Reader, segments []MessageSegment, header *L1IncomingMessageHeader, isTopLevel bool, state *ArbosState) []MessageSegment {
	var l2KindBuf [1]byte
	if _, err := rd.Read(l2KindBuf[:]); err != nil {
		return segments
	}

	switch(l2KindBuf[0]) {
	case L2MessageKind_UnsignedUserTx:
		seg := parseUnsignedTx(rd, header, true, state)
		if seg == nil {
			return segments
		} else {
			return append(segments, seg)
		}
	case L2MessageKind_ContractTx:
		seg := parseUnsignedTx(rd, header, false, state)
		if seg == nil {
			return segments
		} else {
			return append(segments, seg)
		}
	case L2MessageKind_NonmutatingCall:
		panic("unimplemented")
	case L2MessageKind_Batch:
		for {
			nextMsg, err := BytestringFromReader(rd)
			if err != nil {
				return segments
			}
			segments = parseL2Message(bytes.NewReader(nextMsg), segments, header, false, state)
		}
		return segments
	case L2MessageKind_SignedTx:
		panic("unimplemented")
	case L2MessageKind_Heartbeat:
		// do nothing
		return segments
	case L2MessageKind_SignedCompressedTx:
		panic("unimplemented")
	case L2MessageKind_BrotliCompressed:
		if isTopLevel {   // ignore compressed messages if not top level
			decompressed, err := io.ReadAll(io.LimitReader(brotli.NewReader(rd), MaxL2MessageSize))
			if err != nil {
				return segments
			}
			return parseL2Message(bytes.NewReader(decompressed), segments, header, false, state)
		} else {
			return segments
		}
	default:
		// ignore invalid message kind
		return segments
	}
}

func parseUnsignedTx(rd io.Reader, header *L1IncomingMessageHeader, includesNonce bool, state *ArbosState) *unsignedTxSegment {
	gasLimit, err := HashFromReader(rd)
	if err != nil {
		return nil
	}

	gasPrice, err := HashFromReader(rd)
	if err != nil {
		return nil
	}

	var nonce *big.Int
	if includesNonce {
		nonceAsHash, err := HashFromReader(rd)
		if err != nil {
			return nil
		}
		nonce = nonceAsHash.Big()
	}
	//TODO: if nonce isn't supplied, ask geth for the expected nonce and fill it in here?

	destination, err := AddressFrom256FromReader(rd)
	if err != nil {
		return nil
	}

	callvalue, err := HashFromReader(rd)
	if err != nil {
		return nil
	}

	calldata, err := io.ReadAll(rd)
	if err != nil {
		return nil
	}

	return &unsignedTxSegment{
		state,
		gasLimit.Big(),
		gasPrice.Big(),
		nonce,
		destination,
		callvalue.Big(),
		calldata,
	}
}
