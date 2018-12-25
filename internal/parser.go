package internal

import (
	"bufio"
	"bytes"
)

type Parser struct {
	Header     []byte
	PlayerInfo []byte
	CarState   []byte
}

// Returns a new Parser instance.
func NewParser() *Parser {
	return &Parser{
		Header:     make([]byte, 0),
		PlayerInfo: make([]byte, 0),
		CarState:   make([]byte, 0),
	}
}

// Parses a world packet.
func (p *Parser) Parse(packet []byte) {
	fullPacket := clone(packet)
	p.Header = fullPacket[:10]

	packetBody := packet[10:]
	reader := bufio.NewReader(bytes.NewReader(packetBody))

	for {
		pType, err := reader.ReadByte()

		if err != nil {
			break
		}

		pLen, err := reader.ReadByte()

		if err != nil {
			break
		}

		switch pType {
		case 2:
			p.PlayerInfo = make([]byte, pLen)
			reader.Read(p.PlayerInfo)
			break
		case 0x12:
			p.CarState = make([]byte, pLen)
			reader.Read(p.CarState)
			break;
		default:
			reader.Read(make([]byte, pLen))
			break
		}
	}

	fullPacket = nil
}

func (p *Parser) IsOk() bool {
	return len(p.PlayerInfo) > 0 && len(p.CarState) > 0
}

func (p *Parser) IsCarStateOk() bool {
	return len(p.CarState) > 0
}

func (p *Parser) GetPlayerPacket(timeDiff uint16) []byte {
	if p.IsOk() {
		statePosPacket := p.GetStatePosPacket(timeDiff)
		buffer := &bytes.Buffer{}

		buffer.Write(p.Header)
		buffer.Write(p.PlayerInfo)
		buffer.Write(statePosPacket)
		buffer.Write([]byte{0x01,0x01,0x01,0x01})

		return buffer.Bytes()
	}

	return nil
}

func (p *Parser) GetCarStatePacket(timeDiff uint16) []byte {
	if p.IsOk() {
		buffer := &bytes.Buffer{}

		buffer.Write(p.Header)
		buffer.Write(p.CarState)
		buffer.Write([]byte{0x01,0x01,0x01,0x01})

		return buffer.Bytes()
	}

	return nil
}

func (p *Parser) GetStatePosPacket(timeDiff uint16) []byte {
	if p.IsOk() {
		carState := clone(p.CarState)
		carState[2] = byte(timeDiff >> 8)
		carState[3] = byte(timeDiff & 0xFF)

		return carState
	}

	return nil
}
