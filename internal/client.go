package internal

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"runtime/debug"
	"sort"
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
	ControlSeq     uint16
	SyncStopped    bool
	SyncState      SyncState
	SyncDelay      uint16
	Peers          map[byte]*Client
}

func NewClient(instance *Instance, conn *net.UDPConn, address *net.UDPAddr, cliHelloTime uint16) *Client {
	return &Client{
		Address:        address,
		Connection:     conn,
		CliHelloTime:   cliHelloTime,
		Instance:       instance,
		JoinedTime:     time.Now(),
		LastPacketTime: time.Now(),
		ControlSeq:     0,
		SyncState:      SyncStateNone,
		Peers:          make(map[byte]*Client),
	}
}

// GetControlSeq gets the client's latest control-packet sequence number.
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
	} else if data[0] == 1 {
		c.handlePeerToPeer(data)
	} else {
		fmt.Println("UNKNOWN PACKET")
	}
}

func (c *Client) handlePeerToPeer(data []byte) {
	if c.Session == nil || !c.Session.Ready {
		return
	}

	for i := 1; i < len(data)-1; {
		peerId := data[i]
		peerMsgSize := int(binary.BigEndian.Uint16(data[i+1 : i+3]))
		peerMsg := data[i+3 : i+3+peerMsgSize]

		peerClient, peerClientExists := c.Peers[peerId]

		if !peerClientExists {
			panic(fmt.Sprintf("Attempted to send message from client %d[%d] to nonexistent peer %d", c.Session.SessionId, c.SessionSlot, peerId))
		}

		transformedForPeer := peerClient.transformPeerPacket(c, peerMsg)
		peerClient.Send(transformedForPeer)

		i += 3 + peerMsgSize
	}
}

func (c *Client) transformPeerPacket(sender *Client, data []byte) []byte {
	newPacket := make([]byte, len(data)+2)
	newPacket[0] = 1
	newPacket[1] = sender.SessionSlot
	copy(newPacket[2:], fixPostPacket(c, sender, data))

	return newPacket
}

// Handles a SYNC-START packet from the client.
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
		if session.ClientCount == session.MaxClients {
			for _, client := range session.Clients {
				fmt.Printf("Generating peers for client %s\n", client.Address.String())
				otherClients := make([]*Client, 0)
				for _, otherClient := range session.Clients {
					if otherClient == client {
						continue
					}
					otherClients = append(otherClients, otherClient)
				}
				sort.SliceStable(otherClients, func(i, j int) bool {
					return otherClients[i].SessionSlot < otherClients[j].SessionSlot
				})
				for i, otherClient := range otherClients {
					fmt.Printf("\tPeer %d is %s\n", i, otherClient.Address.String())
					client.Peers[byte(i)] = otherClient
				}
			}

			session.Ready = true
		}
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

func fixPostPacket(client *Client, fromClient *Client, packet []byte) []byte {
	timeDiff := client.GetTimeDiff() /*- (client.Ping - fromClient.Ping)*/
	//timeDiff := 0
	packet = clone(packet)
	bodyPtr := 6

	for {
		pktId := packet[bodyPtr]

		if pktId == 0xff {
			break
		}

		pktLen := packet[bodyPtr+1]

		if pktId == 0x12 {
			//fmt.Println("fixing car state time")
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

	return packet
}

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

	// First packet type
	buffer.WriteByte(0)
	// Sequence number
	binary.Write(buffer, binary.BigEndian, c.GetControlSeq())
	// Second packet type
	buffer.WriteByte(2)
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
