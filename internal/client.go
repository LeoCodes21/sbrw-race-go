package internal

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"runtime/debug"
	"time"
)

type SyncState uint

const (
	SyncStateNone      SyncState = 0
	SyncStateStart     SyncState = 1
	SyncStateSync      SyncState = 2
	SyncStateKeepAlive SyncState = 3
)

type Client struct {
	Address        *net.UDPAddr
	CliHelloTime   uint16
	Connection     *net.UDPConn
	Instance       *Instance
	JoinedTime     time.Time
	LastPacketTime time.Time
	Ping           uint16
	Session        *Session
	SessionSlot    byte
	WorldSeq       uint16
	ControlSeq     uint16
	Parser         *Parser
	SyncStopped    bool
	SyncState      SyncState
	SyncDelay      uint16
	PeerSequences  []uint16
}

func NewClient(instance *Instance, conn *net.UDPConn, address *net.UDPAddr, cliHelloTime uint16) *Client {
	return &Client{
		Address:        address,
		Connection:     conn,
		CliHelloTime:   cliHelloTime,
		Instance:       instance,
		JoinedTime:     time.Now(),
		LastPacketTime: time.Now(),
		Parser:         NewParser(),
		WorldSeq:       0,
		ControlSeq:     0,
		SyncState:      SyncStateNone,
		PeerSequences:  make([]uint16, 8),
	}
}

func (c *Client) IsPlayerInfoBeforeOk() bool {
	return c.Parser.IsOk()
}

// Returns the client's latest world-packet sequence number.
func (c *Client) GetWorldSeq() uint16 {
	tmp := c.WorldSeq
	c.WorldSeq++
	//if c.WorldSeq > 32767 {
	//	c.WorldSeq = 0
	//}
	return tmp
}

// Returns the client's latest control-packet sequence number.
func (c *Client) GetControlSeq() uint16 {
	tmp := c.ControlSeq
	c.ControlSeq++
	//if c.ControlSeq > 32767 {
	//	c.ControlSeq = 0
	//}
	return tmp
}

// Returns the difference between the current time and the time the client joined, in milliseconds.
func (c *Client) GetTimeDiff() uint16 {
	return uint16(time.Now().Sub(c.JoinedTime).Seconds() * 1000)
}

// Returns the client's remote port number.
func (c *Client) Port() int {
	return c.Address.Port
}

// Sends a data buffer to the client.
func (c *Client) Send(data []byte) (int, error) {
	return c.Connection.WriteToUDP(data, c.Address)
}

// Processes a data packet from the client.
func (c *Client) HandlePacket(data []byte) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("Error while processing packet: ", r)
			debug.PrintStack()
			fmt.Println(hex.Dump(data))
		}
	}()

	c.Ping = uint16(time.Now().Sub(c.LastPacketTime).Seconds() * 1000)
	c.LastPacketTime = time.Now()

	// SYNC-START: len=26, [3] = 7
	// SYNC: 	   len=22, [3] = 7
	if len(data) == 26 && data[3] == 0x07 {
		c.handleSyncStart(data)
	} else if len(data) == 22 && data[3] == 0x07 {
		c.handleSync()
	} else if len(data) == 18 && data[3] == 0x07 {
		if c.Session == nil {
			fmt.Println("NIL SESSION")
		} else {
			c.SyncState = SyncStateKeepAlive
			c.Session.IncrementSyncCount()
		}
	} else if len(data) > 16 && data[0] == 1 {
		c.handleInfoPackets(data)
	} else {
		fmt.Println("UNKNOWN PACKET")
	}
}

func (c *Client) handleInfoPackets(data []byte) {
	if c.Session == nil {
		fmt.Println("NIL SESSION")
		return
	}

	fragmentIndex := 0

	for i := 1; i < len(data)-1; {
		fragmentLength := int(binary.BigEndian.Uint16(data[i+1 : i+3]))

		if i+3+fragmentLength >= len(data) {
			panic("bad packet fragment :(")
		}

		fragmentData := data[i+3 : i+3+fragmentLength]
		i += 3 + fragmentLength

		for _, c2 := range c.Session.Clients {
			if c2.Port() != c.Port() {
				c2.Send(transformInfoPacket(c2, c, fragmentData))
			}
		}

		fragmentIndex++
	}

	for _, c2 := range c.Session.Clients {
		if c2.Port() != c.Port() {
			c2.PeerSequences[c.SessionSlot]++
		}
	}
}

func fixPostPacket(client *Client, fromClient *Client, packet []byte) ([]byte, int) {
	timeDiff := client.GetTimeDiff() - (client.Ping - fromClient.Ping)
	bodyPtr := 6

	for {
		pktId := packet[bodyPtr]

		if pktId == 0xff {
			break
		}

		pktLen := packet[bodyPtr+1]

		if pktId == 0x12 {
			packet[bodyPtr+2] = byte(timeDiff >> 8)
			packet[bodyPtr+3] = byte(timeDiff & 0xFF)
		} else if pktId == 2 {
			name := string(bytes.Trim(packet[bodyPtr+3:bodyPtr+18], "\x00"))
			if len(name) == 0 {
				for i, c := range trollName {
					packet[bodyPtr+3+i] = byte(c)
				}
			}
		}

		bodyPtr += int(2 + pktLen)
	}

	return packet, bodyPtr
}

func transformInfoPacket(recipient *Client, sender *Client, data []byte) []byte {
	data, dataLen := fixPostPacket(recipient, sender, clone(data))
	newData := make([]byte, 2 /* type ID + opponent ID */ +dataLen+1 /* terminator */ +4 /* checksum */)

	// type ID
	newData[0] = 1
	// opponent ID
	newData[1] = sender.SessionSlot
	// local_seq
	binary.BigEndian.PutUint16(newData[2:4], recipient.GetWorldSeq())

	copy(newData[4:], data[2:dataLen])

	if recipient.SyncStopped {
		//copy(newData[4:6], data[0:2])
		binary.BigEndian.PutUint16(newData[4:6], recipient.PeerSequences[sender.SessionSlot])
	} else {
		binary.BigEndian.PutUint16(newData[4:6], 0xffff)
	}

	binary.BigEndian.PutUint16(newData[6:8], 0xffff)

	newData[len(newData)-5] = 0xff
	newData[len(newData)-4] = 0xDE
	newData[len(newData)-3] = 0xAD
	newData[len(newData)-2] = 0xBE
	newData[len(newData)-1] = 0xEF

	return newData
}

func (c *Client) handleSyncStart(data []byte) {
	c.SyncState = SyncStateStart
	sessionId := binary.BigEndian.Uint32(data[16:20])
	slotByte := data[20]

	session, exists := c.Instance.Sessions[sessionId]

	if !exists {
		c.Instance.Sessions[sessionId] = NewSession(sessionId, (slotByte&0x0F)>>1)
		session = c.Instance.Sessions[sessionId]
	}

	c.SessionSlot = slotByte >> 5
	c.Session = session

	if _, inSession := session.Clients[c.SessionSlot]; !inSession {
		session.Clients[c.SessionSlot] = c
		session.ClientCount++
		c.SendSyncStart()
	}

	session.IncrementSyncCount()
}

func (c *Client) handleSync() {
	if c.Session == nil {
		fmt.Println("NIL SESSION")
		return
	}

	if c.SyncStopped {
		c.SyncState = SyncStateSync
		c.Session.IncrementSyncCount()
	} else {
		c.SyncStopped = true
	}
}

const trollName = "Report Me !"

func (c *Client) SendSyncResponse() {
	switch c.SyncState {
	case SyncStateStart:
		c.SendSyncStart()
		break
	case SyncStateSync:
		c.SendSync()
		break
	case SyncStateKeepAlive:
		c.SendKeepAlive()
		break
	}

	c.SyncState = SyncStateNone
}

func (c *Client) SendHelloResponse() (int, error) {
	buffer := &bytes.Buffer{}

	// First packet type
	buffer.WriteByte(0)
	// Counter
	binary.Write(buffer, binary.BigEndian, c.GetControlSeq())
	// Second packet type
	buffer.WriteByte(1)
	// Time
	binary.Write(buffer, binary.BigEndian, c.CliHelloTime)
	// Cli-time
	binary.Write(buffer, binary.BigEndian, c.CliHelloTime)
	// CRC
	buffer.Write([]byte{0x01, 0x01, 0x01, 0x01})

	return c.Send(buffer.Bytes())
}

func (c *Client) SendSync() (int, error) {
	buffer := &bytes.Buffer{}

	// First packet type
	buffer.WriteByte(0)
	// Sequence number
	binary.Write(buffer, binary.BigEndian, c.GetControlSeq())
	// Second packet type
	buffer.WriteByte(2)

	c.WriteSyncHeader(buffer)

	buffer.Write([]byte{0x01, 0x03, 0x00, 0x4f, 0xed, 0xff})
	buffer.Write([]byte{0x01, 0x01, 0x01, 0x01})

	return c.Send(buffer.Bytes())
}

func (c *Client) SendKeepAlive() (int, error) {
	buffer := &bytes.Buffer{}

	// First packet type
	buffer.WriteByte(0)
	// Sequence number
	binary.Write(buffer, binary.BigEndian, c.GetControlSeq())
	// Second packet type
	buffer.WriteByte(2)

	c.WriteSyncHeader(buffer)

	buffer.Write([]byte{0xff, 0x01, 0x01, 0x01, 0x01})

	return c.Send(buffer.Bytes())
}

func (c *Client) SendSyncStart() (int, error) {
	buffer := &bytes.Buffer{}

	// First packet type
	buffer.WriteByte(0)
	// Sequence number
	binary.Write(buffer, binary.BigEndian, c.GetControlSeq())
	// Second packet type
	buffer.WriteByte(2)

	c.WriteSyncHeader(buffer)

	// Sub-packet
	buffer.WriteByte(0)
	buffer.WriteByte(6)
	buffer.WriteByte(c.SessionSlot)
	binary.Write(buffer, binary.BigEndian, c.Session.SessionId)

	buffer.WriteByte((1 << c.Session.MaxClients) - 1)
	buffer.WriteByte(0xff)
	buffer.Write([]byte{0x01, 0x01, 0x01, 0x01})

	return c.Send(buffer.Bytes())
}

func (c *Client) WriteSyncHeader(buffer *bytes.Buffer) {
	binary.Write(buffer, binary.BigEndian, c.GetTimeDiff())
	binary.Write(buffer, binary.BigEndian, c.CliHelloTime)

	binary.Write(buffer, binary.BigEndian, uint16(c.Session.SyncCount))
	binary.Write(buffer, binary.BigEndian, uint16(0xFFFF)&^(1<<(16-c.Session.SyncCount)))
}
