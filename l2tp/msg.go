package l2tp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/katalix/sl2tpd/internal/nll2tp"
)

// L2TPv2 and L2TPv3 headers have these fields in common
type l2tpCommonHeader struct {
	FlagsVer uint16
	Len      uint16
}

// L2TPv2 control message header per RFC2661
type l2tpV2Header struct {
	Common l2tpCommonHeader
	Tid    uint16
	Sid    uint16
	Ns     uint16
	Nr     uint16
}

// L2TPv3 control message header per RFC3931
type l2tpV3Header struct {
	Common l2tpCommonHeader
	Ccid   uint32
	Ns     uint16
	Nr     uint16
}

const (
	controlMessageMinLen = 12
	controlMessageMaxLen = ^uint16(0)
	commonHeaderLen      = 4
	v2HeaderLen          = 12
	v3HeaderLen          = 12
)

func (h *l2tpCommonHeader) protocolVersion() (version nll2tp.L2tpProtocolVersion, err error) {
	switch h.FlagsVer & 0xf {
	case 2:
		return nll2tp.ProtocolVersion2, nil
	case 3:
		return nll2tp.ProtocolVersion3, nil
	}
	return nll2tp.ProtocolVersionInvalid, errors.New("illegal protocol version")
}

func newL2tpV2ControlMessage(b []byte) (msg *L2tpV2ControlMessage, err error) {
	var hdr l2tpV2Header
	var avps []AVP

	r := bytes.NewReader(b)
	if err = binary.Read(r, binary.BigEndian, &hdr); err != nil {
		return nil, err
	}

	// Messages with no AVP payload are treated as ZLB (zero-length-body) ack messages,
	// so they're valid L2TPv2 messages.  Don't try to parse the AVP payload in this case.
	if hdr.Common.Len > v2HeaderLen {
		if avps, err = ParseAVPBuffer(b[v2HeaderLen:hdr.Common.Len]); err != nil {
			return nil, err
		}
		// RFC2661 says the first AVP in the message MUST be the Message Type AVP,
		// so let's validate that now
		// TODO: we need to do real actual validation
		if avps[0].Type() != AvpTypeMessage {
			return nil, errors.New("Invalid L2TPv2 message: first AVP is not Message Type AVP")
		}
	}

	return &L2tpV2ControlMessage{
		header: hdr,
		avps:   avps,
	}, nil
}

func newL2tpV3ControlMessage(b []byte) (msg *L2tpV3ControlMessage, err error) {
	return nil, errors.New("newL2tpV3ControlMessage not implemented")
}

// L2tpControlMessage is an interface representing a generic L2TP
// control message, providing access to the fields that are common
// to both v2 and v3 versions of the protocol.
type L2tpControlMessage interface {
	ProtocolVersion() nll2tp.L2tpProtocolVersion
	Len() int
	Ns() uint16
	Nr() uint16
	Avps() []AVP
}

// L2tpV2ControlMessage represents an RFC2661 control message
type L2tpV2ControlMessage struct {
	header l2tpV2Header
	avps   []AVP
}

// L2tpV3ControlMessage represents an RFC3931 control message
type L2tpV3ControlMessage struct {
	header l2tpV3Header
	avps   []AVP
}

// ProtocolVersion returns the protocol version for the control message.
// Implements the L2tpControlMessage interface.
func (m *L2tpV2ControlMessage) ProtocolVersion() nll2tp.L2tpProtocolVersion {
	return nll2tp.ProtocolVersion2
}

// Len returns the total control message length, including the header, in octets.
// Implements the L2tpControlMessage interface.
func (m *L2tpV2ControlMessage) Len() int {
	return int(m.header.Common.Len)
}

// Ns returns the L2TP transport Ns value for the message.
// Implements the L2tpControlMessage interface.
func (m *L2tpV2ControlMessage) Ns() uint16 {
	return m.header.Ns
}

// Nr returns the L2TP transport Ns value for the message.
// Implements the L2tpControlMessage interface.
func (m *L2tpV2ControlMessage) Nr() uint16 {
	return m.header.Nr
}

// Avps returns the slice of Attribute Value Pair (AVP) values held by the control message.
// Implements the L2tpControlMessage interface.
func (m *L2tpV2ControlMessage) Avps() []AVP {
	return m.avps
}

// Tid returns the L2TPv2 tunnel ID held by the control message header.
func (m *L2tpV2ControlMessage) Tid() uint16 {
	return m.header.Tid
}

// Sid returns the L2TPv2 session ID held by the control message header.
func (m *L2tpV2ControlMessage) Sid() uint16 {
	return m.header.Sid
}

// ProtocolVersion returns the protocol version for the control message.
// Implements the L2tpControlMessage interface.
func (m *L2tpV3ControlMessage) ProtocolVersion() nll2tp.L2tpProtocolVersion {
	return nll2tp.ProtocolVersion3
}

// Len returns the total control message length, including the header, in octets.
// Implements the L2tpControlMessage interface.
func (m *L2tpV3ControlMessage) Len() int {
	return int(m.header.Common.Len)
}

// Ns returns the L2TP transport Ns value for the message.
// Implements the L2tpControlMessage interface.
func (m *L2tpV3ControlMessage) Ns() uint16 {
	return m.header.Ns
}

// Nr returns the L2TP transport Ns value for the message.
// Implements the L2tpControlMessage interface.
func (m *L2tpV3ControlMessage) Nr() uint16 {
	return m.header.Nr
}

// Avps returns the slice of Attribute Value Pair (AVP) values held by the control message.
// Implements the L2tpControlMessage interface.
func (m *L2tpV3ControlMessage) Avps() []AVP {
	return m.avps
}

// ControlConnectionID returns the control connection ID held by the control message header.
func (m *L2tpV3ControlMessage) ControlConnectionID() uint32 {
	return m.header.Ccid
}

// ParseMessageBuffer takes a byte slice of L2TP control message data and
// parses it into an array of L2tpControlMessage instances.
func ParseMessageBuffer(b []byte) (messages []L2tpControlMessage, err error) {
	r := bytes.NewReader(b)
	for r.Len() >= controlMessageMinLen {
		var h l2tpCommonHeader
		var cursor int64

		if cursor, err = r.Seek(0, io.SeekCurrent); err != nil {
			return nil, errors.New("malformed message buffer: unable to determine current offset")
		}

		// Read the common part of the header: this will tell us the
		// protocol version and the length of the complete frame
		if err := binary.Read(r, binary.BigEndian, &h); err != nil {
			return nil, err
		}

		// Throw out malformed packets
		if int(h.Len-commonHeaderLen) > r.Len() {
			return nil, fmt.Errorf("malformed header: length %d exceeds buffer bounds of %d", h.Len, r.Len())
		}

		// Figure out the protocol version, and read the message
		if ver, err := h.protocolVersion(); err != nil {
			return nil, err
		} else {
			if ver == nll2tp.ProtocolVersion2 {
				if msg, err := newL2tpV2ControlMessage(b[cursor : cursor+int64(h.Len)]); err != nil {
					return nil, err
				} else {
					messages = append(messages, msg)
				}
			} else if ver == nll2tp.ProtocolVersion3 {
				if msg, err := newL2tpV3ControlMessage(b[cursor : cursor+int64(+h.Len)]); err != nil {
					return nil, err
				} else {
					messages = append(messages, msg)
				}
			} else {
				panic("Unhandled protocol version")
			}
		}

		// Step on to the next message in the buffer, if any
		if _, err := r.Seek(int64(h.Len), io.SeekCurrent); err != nil {
			return nil, errors.New("malformed message buffer: invalid length for current message")
		}
	}
	return messages, nil
}
