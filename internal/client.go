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
	}
}

func (c *Client) IsPlayerInfoBeforeOk() bool {
	return c.Parser.IsOk()
}

// Returns the client's latest world-packet sequence number.
func (c *Client) GetWorldSeq() uint16 {
	tmp := c.WorldSeq
	c.WorldSeq++
	if c.WorldSeq > 32767 {
		c.WorldSeq = 0
	}
	return tmp
}

// Returns the client's latest control-packet sequence number.
func (c *Client) GetControlSeq() uint16 {
	tmp := c.ControlSeq
	c.ControlSeq++
	if c.ControlSeq > 32767 {
		c.ControlSeq = 0
	}
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
		c.handleSync(data)
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

	fmt.Printf("Packet from client %d[%d]: %s\n", c.SessionSlot, c.Session.SessionId, hex.EncodeToString(data))
	fragmentIndex := 0

	for i := 1; i < len(data)-1; {
		fragmentLength := int(binary.BigEndian.Uint16(data[i+1 : i+3]))

		if i+3+fragmentLength >= len(data) {
			panic("bad packet fragment :(")
		}

		fragmentData := data[i+3 : i+3+fragmentLength]
		i += 3 + fragmentLength

		fmt.Printf("\tparts[%d]: %s\n", fragmentIndex, hex.EncodeToString(fragmentData))

		for _, c2 := range c.Session.Clients {
			if c2.Port() != c.Port() {
				c2.Send(transformInfoPacket(c2, c, fragmentData))
			}
		}

		fragmentIndex += 1
	}
}

func transformInfoPacket(recipient *Client, sender *Client, data []byte) []byte {
	fmt.Printf("transforming packet from client %d to client %d: %s\n", sender.SessionSlot, recipient.SessionSlot, hex.EncodeToString(data))
	newData := make([]byte, 2 /* type ID + opponent ID */ +len(data))

	// type ID
	newData[0] = 1
	// opponent ID
	newData[1] = sender.SessionSlot
	// sequence
	binary.BigEndian.PutUint16(newData[2:4], recipient.GetWorldSeq())

	// packet
	for i := 2; i < len(data)-5; i++ {
		newData[i+2] = data[i]
	}

	// terminator
	newData[len(newData)-5] = 0xff

	// checksum
	newData[len(newData)-4] = 0x01
	newData[len(newData)-3] = 0x02
	newData[len(newData)-2] = 0x03
	newData[len(newData)-1] = 0x04

	fmt.Printf("transformed packet from client %d to client %d: %s\n", sender.SessionSlot, recipient.SessionSlot, hex.EncodeToString(newData))
	return newData
}

// Handles a SYNC-START packet from the client.
func (c *Client) handleSyncStart(data []byte) {
	c.SyncState = SyncStateStart
	//packetTime := binary.BigEndian.Uint16(data[5:7])
	//packetCliTime := binary.BigEndian.Uint16(data[7:9])
	//syncCounter := binary.BigEndian.Uint16(data[9:11])
	//syncValue := binary.BigEndian.Uint16(data[11:13])
	sessionId := binary.BigEndian.Uint32(data[16:20])
	slotByte := data[20]

	//fmt.Println("SYNC-START:")
	//fmt.Printf("PktTime     = %d\n", packetTime)
	//fmt.Printf("PktCliTime  = %d\n", packetCliTime)
	//fmt.Printf("SyncCounter = %d\n", syncCounter)
	//fmt.Printf("SyncValue   = %d\n", syncValue)
	//fmt.Printf("SessionId   = %d\n", sessionId)
	//fmt.Printf("SlotByte    = %x\n", slotByte)

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
	}

	session.IncrementSyncCount()
}

func (c *Client) handleSync(data []byte) {
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

// Sends a HELLO response packet to the client.
// The packet is 12 bytes long.
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

	//ht, td := c.doSyncWait()

	// First packet type
	buffer.WriteByte(0)
	// Sequence number
	binary.Write(buffer, binary.BigEndian, c.GetControlSeq())
	// Second packet type
	buffer.WriteByte(2)
	// Time
	//binary.Write(buffer, binary.BigEndian, td)
	// Cli-time
	//binary.Write(buffer, binary.BigEndian, ht)

	//c.doSyncWait()
	//
	//buffer.WriteByte(0)
	//binary.Write(buffer, binary.BigEndian, c.GetControlSeq())
	//buffer.WriteByte(2)
	//// Time
	binary.Write(buffer, binary.BigEndian, int16(c.GetTimeDiff()))
	//// Cli-time
	binary.Write(buffer, binary.BigEndian, int16(c.CliHelloTime))
	if c.Session.SyncCount == 0 {
		buffer.Write([]byte{0xFF, 0xFF})
	} else {
		binary.Write(buffer, binary.BigEndian, uint16(c.Session.SyncCount))
	}
	if c.Session.SyncCount == 0 {
		buffer.Write([]byte{0xFF, 0xFF})
	} else {
		binary.Write(buffer, binary.BigEndian, uint16(0xFFFF)&^(1<<(16-c.Session.SyncCount)))
	}

	buffer.Write([]byte{0x01, 0x03, 0x00, 0x4f, 0xed, 0xff})
	buffer.Write([]byte{0x01, 0x01, 0x01, 0x01})

	return c.Send(buffer.Bytes())
}

func (c *Client) SendKeepAlive() (int, error) {
	buffer := &bytes.Buffer{}

	//ht, td := c.doSyncWait()

	// First packet type
	buffer.WriteByte(0)
	// Sequence number
	binary.Write(buffer, binary.BigEndian, c.GetControlSeq())
	// Second packet type
	buffer.WriteByte(2)
	// Time
	//binary.Write(buffer, binary.BigEndian, td)
	// Cli-time
	//binary.Write(buffer, binary.BigEndian, ht)

	//c.doSyncWait()
	//
	//buffer.WriteByte(0)
	//binary.Write(buffer, binary.BigEndian, c.GetControlSeq())
	//buffer.WriteByte(2)
	binary.Write(buffer, binary.BigEndian, int16(c.GetTimeDiff()))
	binary.Write(buffer, binary.BigEndian, int16(c.CliHelloTime))
	if c.Session.SyncCount == 0 {
		buffer.Write([]byte{0xFF, 0xFF})
	} else {
		binary.Write(buffer, binary.BigEndian, uint16(c.Session.SyncCount))
	}
	if c.Session.SyncCount == 0 {
		buffer.Write([]byte{0xFF, 0xFF})
	} else {
		binary.Write(buffer, binary.BigEndian, uint16(0xFFFF)&^(1<<(16-c.Session.SyncCount)))
	}

	buffer.Write([]byte{0xff, 0x01, 0x01, 0x01, 0x01})

	return c.Send(buffer.Bytes())
}

// Sends a SYNC-START response packet to the client.
// The packet is 25 bytes long.
func (c *Client) SendSyncStart() (int, error) {
	buffer := &bytes.Buffer{}

	// First packet type
	buffer.WriteByte(0)
	// Sequence number
	binary.Write(buffer, binary.BigEndian, c.GetControlSeq())
	// Second packet type
	buffer.WriteByte(2)
	// Time
	binary.Write(buffer, binary.BigEndian, int16(c.GetTimeDiff()))
	// Cli-time
	binary.Write(buffer, binary.BigEndian, int16(c.CliHelloTime))
	// Sync-counter
	if c.Session.SyncCount == 0 {
		buffer.Write([]byte{0xFF, 0xFF})
	} else {
		binary.Write(buffer, binary.BigEndian, uint16(c.Session.SyncCount))
	}
	if c.Session.SyncCount == 0 {
		buffer.Write([]byte{0xFF, 0xFF})
	} else {
		binary.Write(buffer, binary.BigEndian, uint16(0xFFFF)&^(1<<(16-c.Session.SyncCount)))
	}

	// Sub-packet
	buffer.WriteByte(0)
	buffer.WriteByte(6)
	buffer.WriteByte(c.SessionSlot)
	binary.Write(buffer, binary.BigEndian, c.Session.SessionId)

	peerMask := byte(0x00)

	for i := byte(0); i < c.Session.MaxClients; i++ {
		peerMask |= 1 << i
	}

	buffer.WriteByte(peerMask)
	buffer.WriteByte(0xff)
	buffer.Write([]byte{0x01, 0x01, 0x01, 0x01})

	return c.Send(buffer.Bytes())
}

// returns ping offset based on other clients
// e.g. client 1 ping 200, client 2 ping 75, client 3 ping 400, client 4 ping 40
// f(client3) = (400 - 200) + (400 - 75) + (400 - 40) = 885
// f(client4) = (40 - 200) + (40 - 75) + (40 - 400) = -555
// return value should be SUBTRACTED from the expression using it, to make lower-lag clients wait for the higher-lag ones
func (c *Client) getPingDiff() int16 {
	var result int16 = 0

	for _, c2 := range c.Session.Clients {
		if c2.Port() != c.Port() {
			result += int16(c.Ping - c2.Ping)
		}
	}

	return result
}

func (c *Client) doSyncWait() (int16, int16) {
	pingDiff := c.getPingDiff()

	if pingDiff < 0 {
		time.Sleep(time.Millisecond * time.Duration(-pingDiff))
	}

	return int16(c.CliHelloTime) + 50, int16(c.GetTimeDiff()) + 50
}
